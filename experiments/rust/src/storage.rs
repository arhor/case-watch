use crate::domain::*;
use chrono::{DateTime, Duration, Utc};
use rusqlite::{Connection, OptionalExtension, params};
use std::sync::{Arc, mpsc};
use tokio::sync::oneshot;
use tokio_util::sync::CancellationToken;
type Job = Box<dyn FnOnce(&mut Connection) + Send>;
struct Worker {
    sender: Option<mpsc::Sender<Job>>,
    join: Option<std::thread::JoinHandle<()>>,
}
impl Drop for Worker {
    fn drop(&mut self) {
        self.sender.take();
        if let Some(j) = self.join.take() {
            let _ = j.join();
        }
    }
}
#[derive(Clone)]
pub struct Store(Arc<Worker>);
fn sql<T>(r: rusqlite::Result<T>) -> Result<T> {
    r.map_err(|_| Error::Storage)
}
fn stamp(t: DateTime<Utc>) -> String {
    t.to_rfc3339_opts(chrono::SecondsFormat::Nanos, true)
}
fn read(c: &Connection, owner: i64) -> Result<Enrollment> {
    let raw=sql(c.query_row("SELECT id,case_number,password,stage,checked_at FROM enrollments WHERE owner_id=? AND deleted_at IS NULL",[owner],|r|Ok((r.get::<_,String>(0)?,r.get::<_,String>(1)?,r.get::<_,String>(2)?,r.get::<_,String>(3)?,r.get::<_,String>(4)?))).optional())?.ok_or(Error::NotFound)?;
    Ok(Enrollment {
        id: raw.0,
        owner,
        number: raw.1,
        password: raw.2,
        stage: raw.3,
        checked_at: DateTime::parse_from_rfc3339(&raw.4)
            .map_err(|_| Error::Storage)?
            .with_timezone(&Utc),
    })
}
impl Store {
    pub fn open(path: &str) -> Result<Self> {
        // Treat environment configuration as a filesystem path, including ':memory:'.
        let path = std::path::absolute(path).map_err(|_| Error::Storage)?;
        Self::start(sql(Connection::open(path))?)
    }
    /// Isolated test storage; production configuration always names a file.
    pub fn memory() -> Result<Self> {
        Self::start(sql(Connection::open_in_memory())?)
    }
    fn start(mut c: Connection) -> Result<Self> {
        sql(c.execute_batch("PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;"))?;
        let v: i64 = sql(c.query_row("PRAGMA user_version", [], |r| r.get(0)))?;
        if v > 1 {
            return Err(Error::Storage);
        }
        if v == 0 {
            let tx = sql(c.transaction())?;
            sql(tx.execute_batch(include_str!("../migrations/000001_initial.up.sql")))?;
            sql(tx.execute_batch("PRAGMA user_version=1"))?;
            sql(tx.commit())?;
        }
        let (sender, receiver) = mpsc::channel::<Job>();
        let join = std::thread::Builder::new()
            .name("sqlite".into())
            .spawn(move || {
                for job in receiver {
                    job(&mut c)
                }
            })
            .map_err(|_| Error::Storage)?;
        Ok(Self(Arc::new(Worker {
            sender: Some(sender),
            join: Some(join),
        })))
    }
    pub async fn execute<T: Send + 'static>(
        &self,
        f: impl FnOnce(&mut Connection) -> Result<T> + Send + 'static,
    ) -> Result<T> {
        let (tx, rx) = oneshot::channel();
        self.0
            .sender
            .as_ref()
            .ok_or(Error::Closed)?
            .send(Box::new(move |c| {
                let _ = tx.send(f(c));
            }))
            .map_err(|_| Error::Storage)?;
        rx.await.map_err(|_| Error::Storage)?
    }
    pub async fn find(&self, owner: i64) -> Result<Enrollment> {
        self.execute(move |c| read(c, owner)).await
    }
    pub async fn owners(&self) -> Result<Vec<i64>> {
        self.execute(|c| {
            let mut s = sql(c.prepare(
                "SELECT owner_id FROM enrollments WHERE deleted_at IS NULL ORDER BY owner_id",
            ))?;
            let rows = sql(s.query_map([], |r| r.get(0)))?;
            sql(rows.collect())
        })
        .await
    }
    pub async fn enroll(&self, e: Enrollment, cancel: CancellationToken) -> Result<()> {
        self.execute(move|c|{
let tx=sql(c.transaction())?;
match read(&tx,e.owner){
Ok(_)=>return Err(Error::Exists),Err(Error::NotFound)=>{
}
,Err(e)=>return Err(e)}
;
sql(tx.execute("INSERT INTO enrollments(id,owner_id,case_number,password,stage,checked_at) VALUES(?,?,?,?,?,?)",params![e.id,e.owner,e.number,e.password,e.stage,stamp(e.checked_at)]))?;
if cancel.is_cancelled(){
return Err(Error::Canceled)}
sql(tx.commit())}
).await
    }
    pub async fn eligibility(
        &self,
        owner: i64,
        id: String,
        manual: bool,
        reserve: Option<DateTime<Utc>>,
        cancel: CancellationToken,
    ) -> Result<(bool, Option<DateTime<Utc>>)> {
        self.execute(move|c|{
 let tx=sql(c.transaction())?;
if read(&tx,owner)?.id!=id{
return Err(Error::NotFound)}
let mut next=None;
 for(kind,subject)in[("case_check",id.clone()),("manual_check",owner.to_string())]{
if kind=="manual_check"&&!manual{
continue}
let start:Option<i64>=sql(tx.query_row("SELECT started_at FROM cooldowns WHERE subject_type=? AND subject_id=?",params![kind,subject],|r|r.get(0)).optional())?;
if let Some(start)=start{
let t=DateTime::from_timestamp_nanos(start)+Duration::seconds(60);
next=Some(next.map_or(t,|n:DateTime<Utc>|n.max(t)));
}
}
 let admitted=reserve.is_some_and(|now|next.is_none_or(|n|now>=n));
if admitted{
let now=reserve.unwrap();
for(kind,subject)in[("case_check",id),("manual_check",owner.to_string())]{
if kind=="manual_check"&&!manual{
continue}
sql(tx.execute("INSERT INTO cooldowns VALUES(?,?,?) ON CONFLICT(subject_type,subject_id) DO UPDATE SET started_at=excluded.started_at",params![kind,subject,now.timestamp_nanos_opt().ok_or(Error::Storage)?]))?;
}
next=Some(now+Duration::seconds(60));
}
if cancel.is_cancelled(){
return Err(Error::Canceled)}
sql(tx.commit())?;
Ok((admitted,next))}
).await
    }
    pub async fn observe(
        &self,
        owner: i64,
        id: String,
        stage: String,
        at: DateTime<Utc>,
        cancel: CancellationToken,
    ) -> Result<()> {
        self.execute(move|c|{
let tx=sql(c.transaction())?;
let old=read(&tx,owner)?;
if old.id!=id{
return Err(Error::NotFound)}
sql(tx.execute("UPDATE enrollments SET stage=?,checked_at=? WHERE id=?",params![stage,stamp(at),id]))?;
if old.stage!=stage{
sql(tx.execute("INSERT INTO history(enrollment_id,previous_stage,stage,checked_at) VALUES(?,?,?,?)",params![id,old.stage,stage,stamp(at)]))?;
}
if cancel.is_cancelled(){
return Err(Error::Canceled)}
sql(tx.commit())}
).await
    }
    pub async fn remove(&self, owner: i64, cancel: CancellationToken) -> Result<()> {
        self.execute(move|c|{
let tx=sql(c.transaction())?;
let e=read(&tx,owner)?;
sql(tx.execute("UPDATE enrollments SET deleted_at=?,case_number=NULL,password=NULL,stage=NULL,checked_at=NULL WHERE id=?",params![stamp(Utc::now()),e.id]))?;
sql(tx.execute("DELETE FROM history WHERE enrollment_id=?",[&e.id]))?;
sql(tx.execute("DELETE FROM cooldowns WHERE subject_type='case_check' AND subject_id=?",[&e.id]))?;
if cancel.is_cancelled(){
return Err(Error::Canceled)}
sql(tx.commit())}
).await
    }
}
