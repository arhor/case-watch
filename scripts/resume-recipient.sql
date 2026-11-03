-- Stop case-watch first. Run with sqlite3 -bail and bind @recipient_id to a
-- positive Telegram user ID; see docs/operations.md for the invocation.
-- Interrupted reservations must be recovered by service startup beforehand.
BEGIN IMMEDIATE;

CREATE TEMP TABLE resume_recipient_guard (
    recipient_id   INTEGER NOT NULL CHECK (typeof(recipient_id) = 'integer' AND recipient_id > 0),
    reserved_count INTEGER NOT NULL CHECK (reserved_count = 0)
);

INSERT OR ROLLBACK INTO resume_recipient_guard (recipient_id, reserved_count)
VALUES (@recipient_id, (SELECT COUNT(*) FROM pending WHERE status = 'reserved'));

INSERT INTO recipients (recipient_id, pause, blocked)
VALUES (@recipient_id, '', 0)
    ON CONFLICT (recipient_id)
    DO UPDATE 
   SET pause = '',
       blocked = 0;

UPDATE pending
   SET cycle_attempts = 0,
       next_attempt = 0,
       failure = ''
 WHERE sequence = (
    SELECT sequence
      FROM pending
     WHERE recipient_id = @recipient_id
       AND status <> 'delivered'
     ORDER BY sequence
     LIMIT 1
 );

DROP TABLE resume_recipient_guard;

COMMIT;
