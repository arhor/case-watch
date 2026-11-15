use crate::{
    domain::*,
    service::{Cases, Outcome},
};
use chrono::{DateTime, Duration as Delta, Utc};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
use tokio::sync::Mutex;
use tokio_util::{sync::CancellationToken, task::TaskTracker};
const COMMANDS: [(&str, &str); 5] = [
    ("start", "Добавить дело для отслеживания"),
    ("status", "Проверить статус дела"),
    ("remove", "Удалить дело и связанные с ним данные"),
    ("cancel", "Отменить текущее действие"),
    ("help", "Показать эту справку"),
];
#[derive(Default)]
struct Draft {
    number: String,
    deadline: Option<DateTime<Utc>>,
    removal: Option<DateTime<Utc>>,
    cancel: Option<CancellationToken>,
}
pub struct Bot {
    client: reqwest::Client,
    url: String,
    cases: Arc<Cases>,
    owners: Mutex<HashMap<i64, Arc<Mutex<Draft>>>>,
    reply_lock: Mutex<()>,
    cancel: CancellationToken,
    tasks: TaskTracker,
    polling: AtomicBool,
    pub unauthorized: AtomicBool,
    messages: HashMap<String, String>,
}
impl Bot {
    pub async fn new(
        token: &str,
        endpoint: &str,
        cases: Arc<Cases>,
        cancel: CancellationToken,
    ) -> Result<Arc<Self>> {
        let bot = Arc::new(Self {
            client: reqwest::Client::builder()
                .retry(reqwest::retry::never())
                .build()
                .map_err(|_| Error::Telegram)?,
            url: format!("{}/bot{}", endpoint.trim_end_matches('/'), token),
            cases,
            owners: Mutex::new(HashMap::new()),
            reply_lock: Mutex::new(()),
            cancel,
            tasks: TaskTracker::new(),
            polling: AtomicBool::new(false),
            unauthorized: AtomicBool::new(false),
            messages: serde_json::from_str(include_str!("messages.json"))
                .map_err(|_| Error::Invalid)?,
        });
        bounded(&bot.cancel, Duration::from_secs(15), async {
            if !bot.api("getMe", json!({}), 15).await?.is_object() {
                return Err(Error::Telegram);
            }
            let commands: Vec<_> = COMMANDS
                .iter()
                .map(|(c, d)| json!({"command":c,"description":d}))
                .collect();
            let r = async {
                let current = bot.api("getMyCommands", json!({}), 15).await?;
                if !current.is_array() {
                    return Err(Error::Telegram);
                }
                if current != json!(commands)
                    && bot
                        .api("setMyCommands", json!({"commands":commands}), 15)
                        .await?
                        != true
                {
                    return Err(Error::Telegram);
                }
                Ok(())
            }
            .await;
            if r == Err(Error::Unauthorized) {
                return r;
            }
            if r.is_err() {
                eprintln!("telegram command synchronization failure")
            }
            Ok(())
        })
        .await?;
        Ok(bot)
    }
    async fn api(&self, method: &str, payload: Value, seconds: u64) -> Result<Value> {
        bounded(&self.cancel, Duration::from_secs(seconds), async {
            let r = self
                .client
                .post(format!("{}/{method}", self.url))
                .json(&payload)
                .send()
                .await
                .map_err(|_| Error::Telegram)?;
            if r.status() == 401 {
                self.unauthorized.store(true, Ordering::SeqCst);
                self.cancel.cancel();
                eprintln!("telegram_invalid_token");
                return Err(Error::Unauthorized);
            }
            let v: Value = r.json().await.map_err(|_| Error::Telegram)?;
            if v["error_code"] == 401 {
                self.unauthorized.store(true, Ordering::SeqCst);
                self.cancel.cancel();
                eprintln!("telegram_invalid_token");
                return Err(Error::Unauthorized);
            }
            if v["error_code"] == 429 {
                return Err(Error::RateLimited(
                    v["parameters"]["retry_after"].as_u64().unwrap_or(0),
                ));
            }
            if v["ok"] != true {
                return Err(Error::Telegram);
            }
            Ok(v["result"].clone())
        })
        .await
    }
    async fn reply(&self, id: i64, text: &str) {
        let _guard = self.reply_lock.lock().await;
        if id <= 0 {
            return;
        }
        if self
            .api("sendMessage", json!({"chat_id":id,"text":text}), 15)
            .await
            .is_err()
        {
            eprintln!("telegram reply failure")
        }
    }
    async fn say(&self, id: i64, index: &str) {
        self.reply(id, &self.messages[index]).await
    }
    async fn erase(&self, id: i64, message: i64) {
        if self
            .api(
                "deleteMessage",
                json!({"chat_id":id,"message_id":message}),
                30,
            )
            .await
            .is_err()
        {
            eprintln!("telegram credential deletion failure")
        }
    }
    pub async fn handle(self: &Arc<Self>, update: &Value) {
        let m = &update["message"];
        let Some(id) = private_owner(m) else { return };
        let message = m["message_id"].as_i64().unwrap_or(0);
        let text = m["text"].as_str().unwrap_or("");
        let draft = self.owners.lock().await.entry(id).or_default().clone();
        let mut s = draft.lock().await;
        let now = self.cases.clock.now();
        let expired = s.deadline.is_some_and(|d| now >= d);
        let expired_remove = s.removal.is_some_and(|d| now >= d);
        if expired {
            s.number.clear();
            s.deadline = None
        }
        if expired_remove {
            s.removal = None
        }
        let words: Vec<_> = text.split_whitespace().collect();
        let command = words
            .first()
            .copied()
            .filter(|s| s.starts_with('/'))
            .unwrap_or("");
        if !command.is_empty() && words.len() > 1 {
            s.number.clear();
            s.deadline = None;
            self.erase(id, message).await;
            self.say(id, "invalid_command").await;
            return;
        }
        match command {
            "/cancel" => {
                if let Some(c) = &s.cancel {
                    c.cancel()
                }
                s.number.clear();
                s.deadline = None;
                s.removal = None;
                self.say(id, "canceled").await;
            }
            "/help" => {
                let mut text = String::from(
                    "CaseWatch помогает следить за ходом рассмотрения вашего дела.\n\nДоступные команды:",
                );
                for (c, d) in COMMANDS {
                    text.push_str(&format!("\n/{c} — {d}"));
                }
                self.reply(id, &text).await;
            }
            "/start" => {
                if let Some(c) = &s.cancel {
                    c.cancel();
                    self.say(id, "cancel_previous").await;
                    return;
                }
                s.number.clear();
                s.deadline = None;
                s.removal = None;
                match self.cases.exists(id).await {
                    Err(_) => self.say(id, "storage_failure").await,
                    Ok(true) => self.say(id, "already_enrolled").await,
                    Ok(false) => {
                        s.deadline = Some(now + Delta::minutes(10));
                        self.say(id, "start").await;
                    }
                }
            }
            "/status" => {
                drop(s);
                let out = self.cases.status(&self.cancel, id, true).await;
                let _s = draft.lock().await;
                if out.error == Some(Error::NotFound) {
                    self.say(id, "not_enrolled").await
                } else {
                    self.reply(id, &format_status(&out)).await
                }
            }
            "/remove" => {
                if let Some(c) = &s.cancel {
                    c.cancel()
                }
                s.number.clear();
                s.deadline = None;
                match self.cases.exists(id).await {
                    Err(_) => self.say(id, "storage_failure_remove").await,
                    Ok(false) => self.say(id, "not_enrolled_remove").await,
                    Ok(true) => {
                        s.removal = Some(now + Delta::minutes(10));
                        self.say(id, "confirm_remove").await;
                    }
                }
            }
            "" => {
                if expired_remove {
                    self.say(id, "expired_removal").await;
                    return;
                }
                if expired {
                    self.say(id, "expired_enrollment").await;
                    return;
                }
                if s.removal.is_some() {
                    match text.trim().to_lowercase().as_str() {
                        "нет" => {
                            s.removal = None;
                            self.say(id, "remove_canceled").await;
                            return;
                        }
                        "да" => {}
                        _ => {
                            self.say(id, "answer_yes_no").await;
                            return;
                        }
                    }
                    s.removal = None;
                    drop(s);
                    let r = self.cases.remove(&self.cancel, id).await;
                    let _s = draft.lock().await;
                    self.say(
                        id,
                        if r.is_ok() {
                            "removed"
                        } else {
                            "remove_failed"
                        },
                    )
                    .await;
                    return;
                }
                if s.deadline.is_none() {
                    self.say(id, "idle").await;
                    return;
                }
                self.erase(id, message).await;
                if s.number.is_empty() {
                    if text.trim().is_empty() || text.len() > 256 || text.contains(['\r', '\n']) {
                        self.say(id, "invalid_number").await;
                        return;
                    }
                    s.number = text.into();
                    s.deadline = Some(now + Delta::minutes(10));
                    self.say(id, "ask_password").await;
                    return;
                }
                if text.is_empty() || text.len() > 4096 {
                    self.say(id, "invalid_password").await;
                    return;
                }
                let number = std::mem::take(&mut s.number);
                s.deadline = None;
                let work = self.cancel.child_token();
                s.cancel = Some(work.clone());
                let password = text.to_string();
                self.say(id, "checking").await;
                let this = self.clone();
                let draft = draft.clone();
                self.tasks.spawn(async move {
                    let r = this.cases.enroll(&work, id, number, password).await;
                    let mut s = draft.lock().await;
                    s.cancel = None;
                    this.say(
                        id,
                        if r.is_ok() {
                            "enrolled"
                        } else {
                            "enroll_failed"
                        },
                    )
                    .await;
                });
            }
            _ => {
                if s.deadline.is_some() {
                    self.erase(id, message).await
                }
                self.say(id, "unknown_command").await;
            }
        }
    }
    pub async fn sweep(&self) {
        let now = self.cases.clock.now();
        for draft in self.owners.lock().await.values() {
            let mut s = draft.lock().await;
            if s.deadline.is_some_and(|d| now >= d) {
                s.deadline = None;
                s.number.clear()
            }
            if s.removal.is_some_and(|d| now >= d) {
                s.removal = None
            }
        }
    }
    pub async fn run(self: &Arc<Self>) -> Result<()> {
        if self.polling.swap(true, Ordering::SeqCst) {
            return Err(Error::Busy);
        }
        let (sender, mut receiver) = tokio::sync::mpsc::channel(1024);
        let this = self.clone();
        let producer = tokio::spawn(async move {
            let mut offset = 1;
            let mut delay = Duration::ZERO;
            loop {
                if !delay.is_zero() {
                    tokio::select! {
                    biased;
                    _=this.cancel.cancelled()=>break,_=tokio::time::sleep(delay)=>{
                    }
                    }
                }
                match this
                    .api(
                        "getUpdates",
                        json!({"offset":offset,"timeout":59,"allowed_updates":["message"]}),
                        60,
                    )
                    .await
                {
                    Ok(updates) => {
                        let Some(updates) = updates.as_array() else {
                            eprintln!("telegram polling failure");
                            delay = backoff(delay);
                            continue;
                        };
                        let mut stuck = false;
                        for u in updates {
                            let Some(id) = u["update_id"].as_i64().filter(|id| *id != 0) else {
                                stuck = true;
                                eprintln!("telegram polling failure");
                                continue;
                            };
                            offset = id.saturating_add(1);
                            stuck = false;
                            tokio::select! {
                            biased;
                            _=this.cancel.cancelled()=>return,_=sender.send(u.clone())=>{
                            }
                            }
                        }
                        delay = if stuck {
                            backoff(delay)
                        } else {
                            Duration::ZERO
                        };
                    }
                    Err(Error::Canceled | Error::Unauthorized) => break,
                    Err(e) => {
                        eprintln!("telegram polling failure");
                        delay = match e {
                            Error::RateLimited(seconds) if seconds > 0 => {
                                Duration::from_secs(seconds)
                            }
                            _ => backoff(delay),
                        };
                    }
                }
            }
        });
        let this = self.clone();
        let sweeper = tokio::spawn(async move {
            let mut sweep = tokio::time::interval(Duration::from_secs(60));
            sweep.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            sweep.tick().await;
            loop {
                tokio::select! {
                biased;
                _=this.cancel.cancelled()=>break, _=sweep.tick()=>this.sweep().await }
            }
        });
        loop {
            tokio::select! {
            biased;
            _=self.cancel.cancelled()=>break,u=receiver.recv()=>{
            match u{
            Some(u)=>self.handle(&u).await,None=>break}
            }
            }
        }
        self.cancel.cancel();
        let _ = producer.await;
        let _ = sweeper.await;
        self.close().await;
        Ok(())
    }
    pub async fn close(&self) {
        for d in self.owners.lock().await.values() {
            let mut s = d.lock().await;
            if let Some(c) = s.cancel.take() {
                c.cancel()
            }
            s.number.clear();
            s.deadline = None;
            s.removal = None;
        }
        self.tasks.close();
        self.tasks.wait().await;
    }
}
pub fn private_owner(m: &Value) -> Option<i64> {
    let id = m["from"]["id"].as_i64()?;
    if id <= 0
        || m["from"]["is_bot"] == true
        || m["chat"]["type"] != "private"
        || m["chat"]["id"].as_i64() != Some(id)
    {
        None
    } else {
        Some(id)
    }
}
pub fn format_status(out: &Outcome) -> String {
    let s = &out.status;
    let mut parts = Vec::new();
    if out.error.is_some() {
        parts.push("Не удалось обновить статус дела.".into())
    }
    if let Some(at) = s.checked_at {
        let stage = if s.stage.is_empty() {
            "Не указан"
        } else {
            &s.stage
        };
        let at = at
            .with_timezone(&chrono_tz::Europe::Warsaw)
            .format("%d.%m.%Y, %H:%M");
        parts.push(if out.error.is_some(){
format!("Показываю данные последней успешной проверки:\n\nСтатус дела: {stage}\nПроверено: {at}")}
else if s.fresh{
format!("Статус дела: {stage}\nПоследняя проверка: {at}\n\nДанные успешно обновлены.")}
else{
format!("Статус дела: {stage}\nПоследняя успешная проверка: {at}")}
);
    } else {
        parts.push("Пока нет данных об успешных проверках вашего дела.".into())
    }
    if s.in_progress {
        parts.push("Проверка статуса ещё выполняется.".into())
    }
    parts.join("\n\n")
}
fn backoff(previous: Duration) -> Duration {
    if previous.is_zero() {
        Duration::from_millis(100)
    } else {
        (previous * 2).min(Duration::from_secs(5))
    }
}
