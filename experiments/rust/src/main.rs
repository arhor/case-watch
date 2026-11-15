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
    let fatal = CancellationToken::new();
    let cases = Cases::new(
        store,
        Arc::new(Gdansk::new("https://klient.gdansk.uw.gov.pl")?),
        Arc::new(SystemClock),
        fatal.clone(),
    );
    let bot = match Bot::new(
        &cfg.token,
        "https://api.telegram.org",
        cases.clone(),
        cancel.clone(),
    )
    .await
    {
        Ok(b) => b,
        Err(Error::Canceled) if cancel.is_cancelled() => return Ok(()),
        Err(e) => return Err(e),
    };
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
