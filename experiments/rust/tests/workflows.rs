use case_watch_rust::{domain::*, service::Cases, storage::Store};
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
struct FakePortal {
    calls: AtomicUsize,
    value: Mutex<Result<String>>,
    gate: tokio::sync::Semaphore,
}
impl Portal for FakePortal {
    fn check<'a>(
        &'a self,
        cancel: &'a CancellationToken,
        _: i64,
        _: &'a str,
        _: &'a str,
    ) -> BoxFuture<'a, Result<String>> {
        Box::pin(async move {
            self.calls.fetch_add(1, Ordering::SeqCst);
            tokio::select! {
            biased;
            _=cancel.cancelled()=>return Err(Error::Canceled),p=self.gate.acquire()=>{
            p.unwrap().forget();
            }
            }
            self.value.lock().unwrap().clone()
        })
    }
    fn forget(&self, _: i64) {}
}
fn setup() -> (Arc<Cases>, Arc<FakePortal>, Arc<ClockAt>) {
    let clock = Arc::new(ClockAt(Mutex::new(
        DateTime::parse_from_rfc3339("2026-11-15T12:00:00Z")
            .unwrap()
            .with_timezone(&Utc),
    )));
    let portal = Arc::new(FakePortal {
        calls: AtomicUsize::new(0),
        value: Mutex::new(Ok("initial".into())),
        gate: tokio::sync::Semaphore::new(100),
    });
    let cases = Cases::new(
        Store::memory().unwrap(),
        portal.clone(),
        clock.clone(),
        CancellationToken::new(),
    );
    (cases, portal, clock)
}
fn advance(clock: &ClockAt) {
    *clock.0.lock().unwrap() += chrono::Duration::seconds(60);
}
#[tokio::test]
async fn cache_failure_cooldowns_history_and_removal() {
    let (c, p, clock) = setup();
    let t = CancellationToken::new();
    c.enroll(&t, 1, "case".into(), "pw".into()).await.unwrap();
    assert_eq!(
        c.enroll(&t, 1, "other".into(), "pw".into()).await,
        Err(Error::Exists)
    );
    let cached = c.status(&t, 1, true).await;
    assert!(!cached.status.fresh);
    assert_eq!(p.calls.load(Ordering::SeqCst), 1);
    advance(&clock);
    *p.value.lock().unwrap() = Err(Error::Transport);
    let failed = c.status(&t, 1, false).await;
    assert!(failed.error.is_some());
    assert_eq!(failed.status.stage, "initial");
    assert!(c.status(&t, 1, true).await.error.is_none());
    assert_eq!(p.calls.load(Ordering::SeqCst), 2);
    advance(&clock);
    *p.value.lock().unwrap() = Ok("changed".into());
    assert!(c.status(&t, 1, true).await.status.fresh);
    let count = c
        .store
        .execute(|db| {
            Ok(db
                .query_row("SELECT count(*) FROM history", [], |r| r.get::<_, i64>(0))
                .unwrap())
        })
        .await
        .unwrap();
    assert_eq!(count, 1);
    c.remove(&t, 1).await.unwrap();
    assert!(!c.exists(1).await.unwrap());
    let counts=c.store.execute(|db|Ok((db.query_row("SELECT count(*) FROM history",[],|r|r.get::<_,i64>(0)).unwrap(),db.query_row("SELECT count(*) FROM cooldowns WHERE subject_type='manual_check'",[],|r|r.get::<_,i64>(0)).unwrap(),db.query_row("SELECT count(*) FROM enrollments WHERE case_number IS NULL AND password IS NULL",[],|r|r.get::<_,i64>(0)).unwrap()))).await.unwrap();
    assert_eq!(counts, (0, 1, 1));
}
#[tokio::test]
async fn concurrent_waiters_share_work_and_removal_fences_commit() {
    let (c, p, clock) = setup();
    let t = CancellationToken::new();
    c.enroll(&t, 1, "case".into(), "pw".into()).await.unwrap();
    advance(&clock);
    p.gate.forget_permits(99);
    let first = CancellationToken::new();
    let c1 = c.clone();
    let f = first.clone();
    let one = tokio::spawn(async move { c1.status(&f, 1, true).await });
    while p.calls.load(Ordering::SeqCst) < 2 {
        tokio::task::yield_now().await;
    }
    let c2 = c.clone();
    let two = tokio::spawn(async move { c2.status(&CancellationToken::new(), 1, false).await });
    tokio::task::yield_now().await;
    first.cancel();
    assert_eq!(one.await.unwrap().error, Some(Error::Canceled));
    assert!(!two.is_finished());
    c.remove(&t, 1).await.unwrap();
    assert_eq!(two.await.unwrap().error, Some(Error::Removing));
    assert!(!c.exists(1).await.unwrap());
    c.shutdown().await;
    assert_eq!(
        c.enroll(&t, 1, "a".into(), "b".into()).await,
        Err(Error::Closed)
    );
}
#[tokio::test]
async fn restart_and_canceled_transaction() {
    let path = std::env::temp_dir().join(format!("case-watch-test-{}.db", uuid::Uuid::new_v4()));
    let t = CancellationToken::new();
    let at = Utc::now();
    let id = uuid::Uuid::new_v4().to_string();
    {
        let s = Store::open(path.to_str().unwrap()).unwrap();
        s.enroll(
            Enrollment {
                id: id.clone(),
                owner: 1,
                number: "x".into(),
                password: "p".into(),
                stage: "old".into(),
                checked_at: at,
            },
            t.clone(),
        )
        .await
        .unwrap();
        s.eligibility(1, id.clone(), true, Some(at), t.clone())
            .await
            .unwrap();
    }
    let s = Store::open(path.to_str().unwrap()).unwrap();
    assert_eq!(s.find(1).await.unwrap().stage, "old");
    assert!(
        !s.eligibility(1, id.clone(), false, Some(at), t.clone())
            .await
            .unwrap()
            .0
    );
    let canceled = CancellationToken::new();
    canceled.cancel();
    assert_eq!(
        s.observe(1, id, "new".into(), at, canceled).await,
        Err(Error::Canceled)
    );
    assert_eq!(s.find(1).await.unwrap().stage, "old");
    drop(s);
    std::fs::remove_file(path).unwrap();
}
#[tokio::test]
async fn last_waiter_cancels_and_shutdown_drains_enrollment() {
    let (c, p, clock) = setup();
    let token = CancellationToken::new();
    c.enroll(&token, 1, "case".into(), "pw".into())
        .await
        .unwrap();
    advance(&clock);
    p.gate.forget_permits(99);
    let waiter = token.clone();
    let copy = c.clone();
    let task = tokio::spawn(async move { copy.status(&waiter, 1, true).await });
    while p.calls.load(Ordering::SeqCst) < 2 {
        tokio::task::yield_now().await;
    }
    token.cancel();
    assert_eq!(task.await.unwrap().error, Some(Error::Canceled));
    c.shutdown().await;
    assert_eq!(c.store.find(1).await.unwrap().stage, "initial");
    let (c, p, _) = setup();
    p.gate.forget_permits(100);
    let copy = c.clone();
    let task = tokio::spawn(async move {
        copy.enroll(&CancellationToken::new(), 2, "case".into(), "pw".into())
            .await
    });
    while p.calls.load(Ordering::SeqCst) < 1 {
        tokio::task::yield_now().await;
    }
    c.shutdown().await;
    assert_eq!(task.await.unwrap(), Err(Error::Canceled));
    assert!(!c.exists(2).await.unwrap());
}
#[tokio::test]
async fn history_failure_rolls_back_observation_and_signals_fatal() {
    let (c, p, clock) = setup();
    let t = CancellationToken::new();
    c.enroll(&t, 1, "case".into(), "pw".into()).await.unwrap();
    c.store.execute(|db|{db.execute_batch("CREATE TRIGGER fail_history BEFORE INSERT ON history BEGIN SELECT RAISE(ABORT,'synthetic'); END;").map_err(|_|Error::Storage)}).await.unwrap();
    advance(&clock);
    *p.value.lock().unwrap() = Ok("changed".into());
    let out = c.status(&t, 1, true).await;
    assert_eq!(out.error, Some(Error::Storage));
    assert_eq!(out.status.stage, "initial");
    assert_eq!(c.store.find(1).await.unwrap().stage, "initial");
    assert!(c.fatal.is_cancelled());
}
#[tokio::test]
async fn canceled_removal_retains_enrollment_and_admission_reopens() {
    let (c, _, _) = setup();
    let t = CancellationToken::new();
    c.enroll(&t, 1, "case".into(), "pw".into()).await.unwrap();
    let canceled = CancellationToken::new();
    canceled.cancel();
    assert_eq!(c.remove(&canceled, 1).await, Err(Error::Canceled));
    assert!(c.exists(1).await.unwrap());
    assert!(c.status(&t, 1, true).await.error.is_none());
}
