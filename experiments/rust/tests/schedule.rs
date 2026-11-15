use case_watch_rust::{
    domain::*,
    schedule::{Jobs, Wait},
    service::Cases,
    storage::Store,
};
use chrono::{DateTime, Utc};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};
use tokio_util::sync::CancellationToken;
struct ClockAt(Mutex<DateTime<Utc>>);
impl Clock for ClockAt {
    fn now(&self) -> DateTime<Utc> {
        *self.0.lock().unwrap()
    }
}
struct Check {
    owners: Mutex<Vec<i64>>,
}
impl Portal for Check {
    fn check<'a>(
        &'a self,
        _: &'a CancellationToken,
        owner: i64,
        _: &'a str,
        _: &'a str,
    ) -> BoxFuture<'a, Result<String>> {
        Box::pin(async move {
            self.owners.lock().unwrap().push(owner);
            Ok("stage".into())
        })
    }
    fn forget(&self, _: i64) {}
}
struct Jump {
    clock: Arc<ClockAt>,
    calls: AtomicUsize,
    late: chrono::Duration,
}
impl Wait for Jump {
    fn until<'a>(
        &'a self,
        cancel: &'a CancellationToken,
        deadline: DateTime<Utc>,
        _: &'a dyn Clock,
    ) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            if self.calls.fetch_add(1, Ordering::SeqCst) == 0 {
                *self.clock.0.lock().unwrap() = deadline + self.late;
                Ok(())
            } else {
                cancel.cancel();
                Err(Error::Canceled)
            }
        })
    }
}
#[tokio::test]
async fn missed_minute_is_skipped_but_jitter_runs_owners_in_order() {
    for (seconds, expected) in [(60, vec![]), (1, vec![1, 2])] {
        let clock = Arc::new(ClockAt(Mutex::new(
            DateTime::parse_from_rfc3339("2026-11-16T06:00:00Z")
                .unwrap()
                .with_timezone(&Utc),
        )));
        let portal = Arc::new(Check {
            owners: Mutex::new(Vec::new()),
        });
        let c = Cases::new(
            Store::memory().unwrap(),
            portal.clone(),
            clock.clone(),
            CancellationToken::new(),
        );
        for id in [2, 1] {
            c.enroll(
                &CancellationToken::new(),
                id,
                "case".into(),
                "password".into(),
            )
            .await
            .unwrap();
        }
        portal.owners.lock().unwrap().clear();
        let wait = Arc::new(Jump {
            clock,
            calls: AtomicUsize::new(0),
            late: chrono::Duration::seconds(seconds),
        });
        let jobs = Jobs::new(wait);
        let _ = jobs.run(c, CancellationToken::new()).await;
        assert_eq!(*portal.owners.lock().unwrap(), expected);
    }
}
#[tokio::test]
async fn overlapping_runs_are_rejected_and_cancel_finishes() {
    let c = Cases::new(
        Store::memory().unwrap(),
        Arc::new(Check {
            owners: Mutex::new(Vec::new()),
        }),
        Arc::new(SystemClock),
        CancellationToken::new(),
    );
    let jobs = Arc::new(Jobs::default());
    let cancel = CancellationToken::new();
    let j = jobs.clone();
    let c1 = c.clone();
    let t = cancel.clone();
    let task = tokio::spawn(async move { j.run(c1, t).await });
    tokio::task::yield_now().await;
    assert_eq!(
        jobs.run(c, CancellationToken::new()).await,
        Err(Error::Busy)
    );
    cancel.cancel();
    assert_eq!(task.await.unwrap(), Err(Error::Canceled));
}
