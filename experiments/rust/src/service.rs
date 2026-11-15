use crate::{domain::*, storage::Store};
use chrono::Duration as Delta;
use std::{collections::HashMap, sync::Arc, time::Duration};
use tokio::sync::{Mutex, watch};
use tokio_util::sync::CancellationToken;
#[derive(Clone)]
pub struct Outcome {
    pub status: Status,
    pub error: Option<Error>,
}
struct Flight {
    cancel: CancellationToken,
    done: watch::Receiver<bool>,
    result: Arc<Mutex<Outcome>>,
    waiters: usize,
    enrollment: bool,
}
#[derive(Default)]
struct Owner {
    flight: Option<Flight>,
    removing: bool,
    removal: Option<watch::Receiver<bool>>,
}
#[derive(Default)]
struct State {
    closed: bool,
    owners: HashMap<i64, Owner>,
}
pub struct Cases {
    pub store: Store,
    portal: Arc<dyn Portal>,
    pub clock: SharedClock,
    state: Mutex<State>,
    pub fatal: CancellationToken,
}
impl Cases {
    pub fn new(
        store: Store,
        portal: Arc<dyn Portal>,
        clock: SharedClock,
        fatal: CancellationToken,
    ) -> Arc<Self> {
        Arc::new(Self {
            store,
            portal,
            clock,
            state: Mutex::new(State::default()),
            fatal,
        })
    }
    fn report<T>(&self, r: Result<T>) -> Result<T> {
        if matches!(r, Err(Error::Storage)) {
            self.fatal.cancel();
        }
        r
    }
    pub async fn exists(&self, owner: i64) -> Result<bool> {
        if owner <= 0 {
            return Err(Error::Denied);
        }
        match self.report(self.store.find(owner).await) {
            Ok(_) => Ok(true),
            Err(Error::NotFound) => Ok(false),
            Err(e) => Err(e),
        }
    }
    pub async fn owners(&self) -> Result<Vec<i64>> {
        self.report(self.store.owners().await)
    }
    pub async fn enroll(
        self: &Arc<Self>,
        cancel: &CancellationToken,
        owner: i64,
        number: String,
        password: String,
    ) -> Result<()> {
        if owner <= 0 {
            return Err(Error::Denied);
        }
        if cancel.is_cancelled() {
            return Err(Error::Canceled);
        }
        if number.trim().is_empty() || password.trim().is_empty() {
            return Err(Error::Invalid);
        }
        let mut state = self.state.lock().await;
        if state.closed {
            return Err(Error::Closed);
        }
        let s = state.owners.entry(owner).or_default();
        if s.removing {
            return Err(Error::Removing);
        }
        if s.flight.is_some() {
            return Err(Error::Busy);
        }
        if self.exists(owner).await? {
            return Err(Error::Exists);
        }
        let work = cancel.child_token();
        let (tx, rx) = watch::channel(false);
        s.flight = Some(Flight {
            cancel: work.clone(),
            done: rx,
            result: Arc::new(Mutex::new(Outcome {
                status: Status::default(),
                error: None,
            })),
            waiters: 1,
            enrollment: true,
        });
        drop(state);
        let r = bounded(
            &work,
            Duration::from_secs(60),
            self.portal.check(&work, owner, &number, &password),
        )
        .await;
        let mut state = self.state.lock().await;
        let s = state.owners.get_mut(&owner).unwrap();
        let result = if s.removing {
            Err(Error::Removing)
        } else if work.is_cancelled() {
            Err(Error::Canceled)
        } else {
            match r {
                Ok(stage) => self.report(
                    self.store
                        .enroll(
                            Enrollment {
                                id: uuid::Uuid::new_v4().to_string(),
                                owner,
                                number,
                                password,
                                stage,
                                checked_at: self.clock.now(),
                            },
                            work.clone(),
                        )
                        .await,
                ),
                Err(Error::Canceled) => Err(Error::Canceled),
                Err(Error::Deadline) => Err(Error::Deadline),
                Err(_) => Err(Error::Check),
            }
        };
        if result.is_err() {
            self.portal.forget(owner)
        }
        s.flight = None;
        let _ = tx.send(true);
        result
    }
    pub async fn status(
        self: &Arc<Self>,
        cancel: &CancellationToken,
        owner: i64,
        manual: bool,
    ) -> Outcome {
        let empty = |e| Outcome {
            status: Status::default(),
            error: Some(e),
        };
        if owner <= 0 {
            return empty(Error::Denied);
        }
        if cancel.is_cancelled() {
            return empty(Error::Canceled);
        }
        let mut state = self.state.lock().await;
        if state.closed {
            return empty(Error::Closed);
        }
        let s = state.owners.entry(owner).or_default();
        if s.removing {
            return empty(Error::Removing);
        }
        let (done, result) = if let Some(f) = s.flight.as_mut() {
            if f.enrollment {
                return empty(Error::Busy);
            }
            f.waiters += 1;
            (f.done.clone(), f.result.clone())
        } else {
            let e = match self.report(self.store.find(owner).await) {
                Ok(e) => e,
                Err(e) => return empty(e),
            };
            let now = self.clock.now();
            let recent = now < e.checked_at + Delta::seconds(60);
            let mut status = Status {
                stage: e.stage.clone(),
                checked_at: Some(e.checked_at),
                ..Default::default()
            };
            let (admitted, next) = match self.report(
                self.store
                    .eligibility(
                        owner,
                        e.id.clone(),
                        manual,
                        if recent { None } else { Some(now) },
                        cancel.clone(),
                    )
                    .await,
            ) {
                Ok(v) => v,
                Err(e) => {
                    return Outcome {
                        status,
                        error: Some(e),
                    };
                }
            };
            status.next_eligible_at = next;
            if recent {
                status.next_eligible_at =
                    Some(next.map_or(e.checked_at + Delta::seconds(60), |n| {
                        n.max(e.checked_at + Delta::seconds(60))
                    }));
            }
            if !admitted {
                return Outcome {
                    status,
                    error: None,
                };
            }
            let work = CancellationToken::new();
            let (tx, rx) = watch::channel(false);
            let result = Arc::new(Mutex::new(Outcome {
                status,
                error: None,
            }));
            s.flight = Some(Flight {
                cancel: work.clone(),
                done: rx.clone(),
                result: result.clone(),
                waiters: 1,
                enrollment: false,
            });
            let this = self.clone();
            let output = result.clone();
            tokio::spawn(async move {
                let r = bounded(
                    &work,
                    Duration::from_secs(60),
                    this.portal.check(&work, owner, &e.number, &e.password),
                )
                .await
                .map_err(|e| match e {
                    Error::Canceled | Error::Deadline => e,
                    _ => Error::Check,
                });
                // The service deadline can drop the adapter future before its cleanup.
                if r.is_err() {
                    this.portal.forget(owner);
                }
                let at = this.clock.now();
                let mut state = this.state.lock().await;
                let s = state.owners.get_mut(&owner).unwrap();
                let mut out = output.lock().await;
                let r = if s.removing {
                    Err(Error::Removing)
                } else if work.is_cancelled() {
                    Err(Error::Canceled)
                } else {
                    r
                };
                match r {
                    Ok(stage) => {
                        match this.report(
                            this.store
                                .observe(owner, e.id, stage.clone(), at, work.clone())
                                .await,
                        ) {
                            Ok(()) => {
                                out.status.stage = stage;
                                out.status.checked_at = Some(at);
                                out.status.fresh = true;
                                out.status.next_eligible_at = Some(
                                    out.status
                                        .next_eligible_at
                                        .unwrap_or(at)
                                        .max(at + Delta::seconds(60)),
                                );
                            }
                            Err(e) => out.error = Some(e),
                        }
                    }
                    Err(e) => out.error = Some(e),
                }
                s.flight = None;
                let _ = tx.send(true);
            });
            (rx, result)
        };
        drop(state);
        let mut done = done;
        let canceled = tokio::select! {
        biased;
        _=done.wait_for(|v|*v)=>false,_=cancel.cancelled()=>true};
        if canceled {
            let mut state = self.state.lock().await;
            if let Some(f) = state.owners.get_mut(&owner).and_then(|s| s.flight.as_mut()) {
                f.waiters -= 1;
                if f.waiters == 0 {
                    f.cancel.cancel();
                }
            }
            drop(state);
            let mut out = result.lock().await.clone();
            out.status.in_progress = true;
            out.error = Some(Error::Canceled);
            if !manual {
                let _ = done.wait_for(|v| *v).await;
            }
            return out;
        }
        result.lock().await.clone()
    }
    pub async fn remove(&self, cancel: &CancellationToken, owner: i64) -> Result<()> {
        if owner <= 0 {
            return Err(Error::Denied);
        }
        if cancel.is_cancelled() {
            return Err(Error::Canceled);
        }
        let mut state = self.state.lock().await;
        if state.closed {
            return Err(Error::Closed);
        }
        let s = state.owners.entry(owner).or_default();
        if s.removing {
            return Err(Error::Removing);
        }
        s.removing = true;
        let (tx, rx) = watch::channel(false);
        s.removal = Some(rx);
        let done = s.flight.as_ref().map(|f| {
            f.cancel.cancel();
            f.done.clone()
        });
        drop(state);
        if let Some(mut d) = done {
            let _ = d.wait_for(|v| *v).await;
        }
        let mut state = self.state.lock().await;
        let r = if cancel.is_cancelled() {
            Err(Error::Canceled)
        } else {
            self.report(self.store.remove(owner, cancel.clone()).await)
        };
        if r.is_ok() {
            self.portal.forget(owner)
        }
        let s = state.owners.get_mut(&owner).unwrap();
        s.removing = false;
        s.removal = None;
        let _ = tx.send(true);
        r
    }
    pub async fn shutdown(&self) {
        let mut state = self.state.lock().await;
        state.closed = true;
        let mut waits = Vec::new();
        for s in state.owners.values() {
            if let Some(f) = &s.flight {
                f.cancel.cancel();
                waits.push(f.done.clone())
            }
            if let Some(d) = &s.removal {
                waits.push(d.clone())
            }
        }
        drop(state);
        for mut d in waits {
            let _ = d.wait_for(|v| *v).await;
        }
    }
}
