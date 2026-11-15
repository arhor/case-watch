use crate::{domain::*, service::Cases};
use chrono::{DateTime, Datelike, Days, TimeZone, Utc, Weekday};
use std::sync::Arc;
use tokio_util::sync::CancellationToken;
pub fn next(after: DateTime<Utc>) -> DateTime<Utc> {
    let tz = chrono_tz::Europe::Warsaw;
    let mut day = after.with_timezone(&tz).date_naive();
    loop {
        if !matches!(day.weekday(), Weekday::Sat | Weekday::Sun) {
            for hour in [8, 10, 12, 14, 16, 18] {
                let slot = tz
                    .from_local_datetime(&day.and_hms_opt(hour, 0, 0).unwrap())
                    .single()
                    .unwrap()
                    .with_timezone(&Utc);
                if slot > after {
                    return slot;
                }
            }
        }
        day = day.checked_add_days(Days::new(1)).unwrap();
    }
}
async fn run_loop(cases: Arc<Cases>, cancel: CancellationToken, wait: &dyn Wait) -> Result<()> {
    loop {
        let slot = next(cases.clock.now());
        wait.until(&cancel, slot, cases.clock.as_ref()).await?;
        if cancel.is_cancelled() {
            return Ok(());
        }
        let now = cases.clock.now();
        if now < slot || now >= slot + chrono::Duration::minutes(1) {
            continue;
        }
        for owner in cases.owners().await? {
            if cancel.is_cancelled() {
                return Ok(());
            }
            if cases.status(&cancel, owner, false).await.error.is_some() {
                eprintln!("check_failed")
            }
        }
    }
}
pub trait Wait: Send + Sync {
    fn until<'a>(
        &'a self,
        cancel: &'a CancellationToken,
        deadline: DateTime<Utc>,
        clock: &'a dyn Clock,
    ) -> BoxFuture<'a, Result<()>>;
}
struct Timer;
impl Wait for Timer {
    fn until<'a>(
        &'a self,
        cancel: &'a CancellationToken,
        deadline: DateTime<Utc>,
        clock: &'a dyn Clock,
    ) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let delay = (deadline - clock.now()).to_std().unwrap_or_default();
            tokio::select! {biased;_=cancel.cancelled()=>Err(Error::Canceled),_=tokio::time::sleep(delay)=>Ok(())}
        })
    }
}
pub struct Jobs {
    running: std::sync::atomic::AtomicBool,
    wait: Arc<dyn Wait>,
}
impl Default for Jobs {
    fn default() -> Self {
        Self::new(Arc::new(Timer))
    }
}
impl Jobs {
    pub fn new(wait: Arc<dyn Wait>) -> Self {
        Self {
            running: std::sync::atomic::AtomicBool::new(false),
            wait,
        }
    }
    pub async fn run(&self, cases: Arc<Cases>, cancel: CancellationToken) -> Result<()> {
        use std::sync::atomic::Ordering;
        if self.running.swap(true, Ordering::SeqCst) {
            return Err(Error::Busy);
        }
        struct Reset<'a>(&'a std::sync::atomic::AtomicBool);
        impl Drop for Reset<'_> {
            fn drop(&mut self) {
                self.0.store(false, std::sync::atomic::Ordering::SeqCst);
            }
        }
        let _guard = Reset(&self.running);
        run_loop(cases, cancel, self.wait.as_ref()).await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn slots_are_strict_and_follow_warsaw_dst() {
        let parse = |s: &str| DateTime::parse_from_rfc3339(s).unwrap().with_timezone(&Utc);
        assert_eq!(
            next(parse("2026-03-27T17:00:00Z")),
            parse("2026-03-30T06:00:00Z")
        );
        assert_eq!(
            next(parse("2026-10-23T16:00:00Z")),
            parse("2026-10-26T07:00:00Z")
        );
        assert_eq!(
            next(parse("2026-11-16T07:00:00Z")),
            parse("2026-11-16T09:00:00Z")
        );
    }
}
