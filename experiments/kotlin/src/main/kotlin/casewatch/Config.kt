package casewatch

/** Syntax validation never includes supplied values in exceptions. */
data class Config(
    val telegramBotToken: String,
    val dbPath: String,
) {
    companion object {
        fun load(lookup: (String) -> String? = System::getenv): Config {
            val token = lookup("CASE_WATCH_TELEGRAM_BOT_TOKEN").orEmpty()
            require(Regex("^[1-9][0-9]*:[A-Za-z0-9_-]{35}$").matches(token)) {
                "CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax"
            }
            val path = lookup("CASE_WATCH_DB_PATH").orEmpty()
            require(path.isNotBlank() && path.none { it == '\u0000' || it == '\r' || it == '\n' }) {
                "CASE_WATCH_DB_PATH: invalid or missing path"
            }
            return Config(token, path)
        }
    }
}
