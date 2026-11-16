use case_watch_rust::{
    config::Config, domain::*, portal::Gdansk, schedule, service::Cases, storage::Store,
    telegram::Bot,
};
use std::sync::{Arc, atomic::Ordering};
use tokio_util::sync::CancellationToken;
fn main() {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .max_blocking_threads(1)
        .build()
        .expect("runtime initialization");
    if let Err(e) = runtime.block_on(run()) {
        eprintln!("{e}");
        std::process::exit(1)
    }
}
async fn run() -> Result<()> {
    let cfg = Config::load(|k| std::env::var(k).ok())?;
    let cancel = CancellationToken::new();
    let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        .map_err(|_| Error::Closed)?;
    let signal = cancel.clone();
    let signals = tokio::spawn(async move {
        tokio::select! {_=tokio::signal::ctrl_c()=>{},_=term.recv()=>{}}
        signal.cancel();
    });
    let result = application(cfg, cancel).await;
    signals.abort();
    let _ = signals.await;
    result
}
async fn application(cfg: Config, cancel: CancellationToken) -> Result<()> {
    let store = Store::open(&cfg.db_path)?;
    store.enroll(Enrollment { id: "00000000000000000000000000000001".into(), owner: 1, number: "synthetic-number".into(), password: "synthetic-password".into(), stage: "Synthetic stage".into(), checked_at: chrono::Utc::now() - chrono::Duration::minutes(2) }, cancel.clone()).await?;
    let fatal = CancellationToken::new();
    let cases = Cases::new(
        store,
        Arc::new(Gdansk::new(&std::env::var("BENCH_ENDPOINT").unwrap())?),
        Arc::new(SystemClock),
        fatal.clone(),
    );
    let bot = match Bot::new(
        &cfg.token,
        &std::env::var("BENCH_ENDPOINT").unwrap(),
        cases.clone(),
        cancel.clone(),
    )
    .await
    {
        Ok(b) => b,
        Err(Error::Canceled) if cancel.is_cancelled() => return Ok(()),
        Err(e) => return Err(e),
    };
    if std::env::var("BENCH_SCENARIO").unwrap() == "scheduled" {
        let c = cases.clone(); let stop = cancel.clone();
        tokio::spawn(async move {
            tokio::time::sleep(std::time::Duration::from_secs(10)).await;
            for owner in c.owners().await.unwrap() {
                assert!(c.status(&stop, owner, false).await.error.is_none());
            }
            reqwest::Client::new().post(format!("{}/scheduled-done", std::env::var("BENCH_ENDPOINT").unwrap())).send().await.unwrap();
        });
    }
    let b = bot.clone();
    let mut polling = tokio::spawn(async move { b.run().await });
    let c = cases.clone();
    let stop = cancel.clone();
    let mut scheduled = tokio::spawn(async move { schedule::Jobs::default().run(c, stop).await });
    let failed = tokio::select! {_=fatal.cancelled()=>true,_=cancel.cancelled()=>false,r=&mut polling=>!cancel.is_cancelled()||!matches!(r,Ok(Ok(()))),r=&mut scheduled=>!cancel.is_cancelled()||!matches!(r,Ok(Ok(()))|Ok(Err(Error::Canceled)))};
    cancel.cancel();
    cases.shutdown().await;
    if !polling.is_finished() {
        let _ = polling.await;
    }
    if !scheduled.is_finished() {
        let _ = scheduled.await;
    }
    bot.close().await;
    if failed || fatal.is_cancelled() || bot.unauthorized.load(Ordering::SeqCst) {
        Err(Error::Closed)
    } else {
        Ok(())
    }
}
