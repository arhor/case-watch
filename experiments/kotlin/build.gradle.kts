plugins {
    kotlin("jvm") version "2.4.21"
    kotlin("plugin.serialization") version "2.4.21"
    application
}
repositories { mavenCentral() }
kotlin { jvmToolchain(25) }
dependencies {
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.13.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.xerial:sqlite-jdbc:3.54.1.0")
    testImplementation(kotlin("test"))
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
}
dependencyLocking { lockAllConfigurations() }
tasks.test {
    useJUnitPlatform()
    jvmArgs("--enable-native-access=ALL-UNNAMED")
}
application {
    mainClass.set("casewatch.MainKt")
    applicationDefaultJvmArgs = listOf("--enable-native-access=ALL-UNNAMED")
}
tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    compilerOptions.allWarningsAsErrors.set(true)
}
val formatter = configurations.create("formatter")
dependencies { formatter("com.pinterest.ktlint:ktlint-cli:1.5.0") }
tasks.register<JavaExec>("formatCheck") {
    classpath = formatter
    mainClass.set("com.pinterest.ktlint.Main")
    args("src/**/*.kt", "*.gradle.kts")
}
tasks.register<JavaExec>("formatApply") {
    classpath = formatter
    mainClass.set("com.pinterest.ktlint.Main")
    args("-F", "src/**/*.kt", "*.gradle.kts")
}
