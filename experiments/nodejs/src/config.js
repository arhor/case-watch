export function loadConfig(env = process.env) {
  const token = env.CASE_WATCH_TELEGRAM_BOT_TOKEN;
  const dbPath = env.CASE_WATCH_DB_PATH;
  if (!/^[1-9][0-9]*:[A-Za-z0-9_-]{35}(?![\s\S])/.test(token ?? ""))
    throw new Error(
      "CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax",
    );
  if (!dbPath?.trim() || /[\0\r\n]/.test(dbPath))
    throw new Error("CASE_WATCH_DB_PATH: invalid or missing path");
  return { token, dbPath };
}
