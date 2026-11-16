from pathlib import Path
root=Path(__file__).resolve().parents[2]
out=Path(__file__).resolve().parent
s=(root/'experiments/rust/src/main.rs').read_text()
s=s.replace('"https://klient.gdansk.uw.gov.pl"','&std::env::var("BENCH_ENDPOINT").unwrap()').replace('"https://api.telegram.org"','&std::env::var("BENCH_ENDPOINT").unwrap()')
s=s.replace('let fatal = CancellationToken::new();','''store.enroll(Enrollment { id: "00000000000000000000000000000001".into(), owner: 1, number: "synthetic-number".into(), password: "synthetic-password".into(), stage: "Synthetic stage".into(), checked_at: chrono::Utc::now() - chrono::Duration::minutes(2) }, cancel.clone()).await?;
    let fatal = CancellationToken::new();''')
s=s.replace('let b = bot.clone();','''if std::env::var("BENCH_SCENARIO").unwrap() == "scheduled" {
        let c = cases.clone(); let stop = cancel.clone();
        tokio::spawn(async move {
            tokio::time::sleep(std::time::Duration::from_secs(10)).await;
            for owner in c.owners().await.unwrap() {
                assert!(c.status(&stop, owner, false).await.error.is_none());
            }
            reqwest::Client::new().post(format!("{}/scheduled-done", std::env::var("BENCH_ENDPOINT").unwrap())).send().await.unwrap();
        });
    }
    let b = bot.clone();''')
(out/'rust-main.rs').write_text(s)
s=(root/'experiments/kotlin/src/main/kotlin/casewatch/Main.kt').read_text()
s=s.replace('PortalClient().use','PortalClient(base = System.getenv("BENCH_ENDPOINT")).use')
s=s.replace('val cases = CaseService(store, portal)','''store.create(Enrollment("00000000000000000000000000000001", 1, "synthetic-number", "synthetic-password", Observation("Synthetic stage", java.time.Instant.now().minusSeconds(120))))
                        val cases = CaseService(store, portal)''')
s=s.replace('Telegram(config.telegramBotToken, cases)','Telegram(config.telegramBotToken, cases, endpoint = System.getenv("BENCH_ENDPOINT"))')
s=s.replace('launch { telegram.run() }','''launch { telegram.run() }
                                    if (System.getenv("BENCH_SCENARIO") == "scheduled") launch {
                                        delay(10000)
                                        for (owner in cases.scheduledOwners()) check(cases.status(owner, false).failure == null)
                                        withContext(Dispatchers.IO) {
                                            val connection = java.net.URI(System.getenv("BENCH_ENDPOINT") + "/scheduled-done").toURL().openConnection() as java.net.HttpURLConnection
                                            connection.requestMethod = "POST"
                                            connection.inputStream.close()
                                            connection.disconnect()
                                        }
                                    }''')
(out/'kotlin-Main.kt').write_text(s)
