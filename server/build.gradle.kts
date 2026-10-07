// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

plugins {
    kotlin("jvm")
    kotlin("plugin.spring")
    kotlin("plugin.jpa")
    id("org.springframework.boot")
    id("io.spring.dependency-management")
    id("dev.detekt")
    id("com.diffplug.spotless")
    id("io.github.anschnapp.mutflow")
    jacoco
}

group = "dev.sard"
version = providers.gradleProperty("sardVersion").getOrElse("dev")

// Kotlin 2.4.20 overrides the 2.3.x managed by Spring Boot 4.1.1 (owner's decision, see session log).
extra["kotlin.version"] = "2.4.20"

kotlin {
    jvmToolchain(25)
    compilerOptions {
        freeCompilerArgs.addAll("-Xjsr305=strict")
    }
}

// mutflow's "mutatedMain" source set is a separate Kotlin compilation, so by default it
// gets its own module name and its own mangled names for `internal` declarations (a
// module-private disambiguation Kotlin adds to the JVM method name). :server:test then
// runs against a classpath where "main" is swapped for "mutatedMain": any test calling an
// `internal` member compiled against "main" would otherwise get a NoSuchMethodError against
// the differently-mangled "mutatedMain" class. Pinning the same module name for every
// Kotlin compilation of this project (main, mutatedMain, test) keeps the mangled names
// identical, so the swap does not break `internal` linkage.
tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    compilerOptions {
        moduleName.set("sard_server")
    }
}

// Kotlin test class file names come from Cyrillic spec-quoted scenario titles; the
// JVM derives file-name encoding (sun.jnu.encoding) from the Gradle daemon's locale
// at startup, not from JVM flags on the compile task. Fail fast with a clear message
// instead of letting compilation fail (or silently mangle file names) on a stale
// daemon started without a UTF-8 locale.
tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    doFirst {
        check(System.getProperty("sun.jnu.encoding").equals("UTF-8", ignoreCase = true)) {
            "$name needs a UTF-8 locale: export LC_ALL=C.UTF-8 and run ./gradlew --stop"
        }
    }
}

dependencies {
    implementation("org.springframework.boot:spring-boot-starter-webmvc")
    implementation("org.springframework.boot:spring-boot-starter-actuator")
    implementation("org.springframework.boot:spring-boot-starter-data-jpa")
    implementation("org.springframework.boot:spring-boot-starter-flyway")
    implementation("org.springframework.boot:spring-boot-starter-grpc-server")
    // Rich gRPC status details (google.rpc.ErrorInfo): already on the classpath transitively
    // through spring-boot-starter-grpc-server; declared directly because EnrollmentStatus uses it.
    implementation("io.grpc:grpc-protobuf")
    implementation("org.flywaydb:flyway-database-postgresql")
    implementation("org.bouncycastle:bcpkix-jdk18on:1.86")
    implementation("com.google.api.grpc:proto-google-common-protos:2.64.1")
    implementation("org.springdoc:springdoc-openapi-starter-webmvc-api:3.1.1")
    implementation("com.networknt:json-schema-validator:3.0.8")
    implementation("tools.jackson.module:jackson-module-kotlin")
    implementation("org.jetbrains.kotlin:kotlin-reflect")
    implementation(project(":proto-jvm"))
    runtimeOnly("org.postgresql:postgresql")

    testImplementation("org.springframework.boot:spring-boot-starter-webmvc-test")
    testImplementation("org.springframework.boot:spring-boot-starter-grpc-server-test")
    testImplementation("org.springframework.boot:spring-boot-testcontainers")
    testImplementation("org.testcontainers:testcontainers-junit-jupiter")
    testImplementation("org.testcontainers:testcontainers-postgresql")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit5")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

springBoot {
    buildInfo()
}

tasks.bootJar {
    archiveFileName = "sard-server.jar"
}

// The console (S10, ADR 0040): the jar carries web/dist under console/ only when -PsardConsoleDist=<dir>
// names a finished build (make build and the image's Dockerfile do); without it the jar has no console
// and needs no Node. A directory without index.html fails the build, naming the property and the directory.
val consoleDist = providers.gradleProperty("sardConsoleDist")
val verifyConsoleDist by tasks.registering {
    val dist = consoleDist.map { rootProject.layout.projectDirectory.dir(it) }
    onlyIf { dist.isPresent }
    doFirst {
        val directory = dist.get().asFile
        check(directory.resolve("index.html").isFile) {
            "-PsardConsoleDist=${consoleDist.get()}: $directory has no index.html; build the console first (cd web && npm run build)"
        }
    }
}
tasks.processResources {
    if (consoleDist.isPresent) {
        dependsOn(verifyConsoleDist)
        from(consoleDist.map { rootProject.layout.projectDirectory.dir(it) }) { into("console") }
    }
}

// The real sard-agent for the agent seam tests (S4a), built once per build and shared by every
// test that runs it. Needs the Go toolchain on PATH. CI does not build it here: it passes the
// agent of its release packages, -PsardTestAgentBinary and the version it was built with,
// -PsardTestAgentVersion, so the seam tests run the agent that ships (ADR 0045).
val testAgentBinary = layout.buildDirectory.file("test-agent/sard-agent")
val prebuiltTestAgent: String? = providers.gradleProperty("sardTestAgentBinary").orNull
val testAgentVersion: String =
    if (prebuiltTestAgent == null) {
        "seam-test"
    } else {
        providers.gradleProperty("sardTestAgentVersion").orNull
            ?: error("-PsardTestAgentBinary needs -PsardTestAgentVersion, the version that agent was built with")
    }
val buildTestAgent by tasks.registering(Exec::class) {
    val repo = rootProject.layout.projectDirectory
    workingDir = repo.asFile
    inputs.files(fileTree(repo.dir("agent")) { include("**/*.go", "**/go.mod", "**/go.sum", "**/*.json") })
    inputs.files(fileTree(repo.dir("proto/gen/go")) { include("**/*.go", "go.mod", "go.sum") })
    inputs.files(repo.file("go.work"), repo.file("go.work.sum"))
    outputs.file(testAgentBinary)
    commandLine(
        "go",
        "build",
        "-ldflags",
        "-X main.version=$testAgentVersion",
        "-o",
        testAgentBinary.get().asFile.path,
        "./agent/cmd/sard-agent",
    )
}

tasks.test {
    useJUnitPlatform()
    if (prebuiltTestAgent == null) dependsOn(buildTestAgent)
    systemProperty("sard.test.agent-binary", prebuiltTestAgent ?: testAgentBinary.get().asFile.path)
    systemProperty("sard.test.agent-version", testAgentVersion)
    // The agent refuses to start without a restic it accepts (A5b, ADR 0017); the seam tests
    // give it a stand-in that reports the minimum version from this file.
    systemProperty(
        "sard.test.restic-version-file",
        rootProject.file("agent/internal/restic/restic-version").path,
    )
    // The release order of versions, shared with the dpkg and rpm checks (ADR 0047).
    systemProperty("sard.test.version-order-file", rootProject.file("deploy/release/version-order.txt").path)
    // Tags that are not releases, shared with scripts/test-release-version.sh (ADR 0047).
    systemProperty("sard.test.not-release-tags-file", rootProject.file("deploy/release/not-release-tags.txt").path)
    // The CA of integration tests; the default /var/lib/sard/pki is not writable here.
    systemProperty(
        "sard.pki.dir",
        layout.buildDirectory
            .dir("test-pki")
            .get()
            .asFile.path,
    )
    // CSRF tests (W1b) set the Host header explicitly to control the expected Origin;
    // java.net.http.HttpClient refuses to set it unless this is allowed.
    systemProperty("jdk.httpclient.allowRestrictedHeaders", "host")
    // A default so every test that boots the full context, not just the session ones,
    // does not need its own SARD_ADMIN_PASSWORD; session tests override it per class.
    environment("SARD_ADMIN_PASSWORD", "test-admin-password-2026")
    // No agent release next to the test server; AgentDownloadsIntegrationTest switches downloads on.
    environment("SARD_AGENT_DOWNLOADS", "false")
    finalizedBy(tasks.jacocoTestReport)
}

jacoco {
    toolVersion = "0.8.15"
}

tasks.jacocoTestReport {
    dependsOn(tasks.test)
    reports {
        xml.required = true
        html.required = false
    }
}

tasks.jacocoTestCoverageVerification {
    dependsOn(tasks.jacocoTestReport)
    violationRules {
        rule {
            limit {
                counter = "INSTRUCTION"
                minimum = "0.80".toBigDecimal()
            }
        }
    }
}

tasks.check {
    dependsOn(tasks.jacocoTestCoverageVerification)
}

// detekt runs on its own embedded compiler (built with Kotlin 2.4.10); keep the
// project's Kotlin 2.4.20 from leaking into its classpath.
configurations.matching { it.name == "detekt" }.configureEach {
    resolutionStrategy.eachDependency {
        if (requested.group == "org.jetbrains.kotlin") useVersion("2.4.10")
    }
}

detekt {
    buildUponDefaultConfig = true
    config.setFrom(files("config/detekt.yml"))
    source.setFrom("src/main/kotlin", "src/test/kotlin")
}

spotless {
    kotlin {
        target("src/**/*.kt")
        ktlint()
    }
    kotlinGradle {
        target("*.gradle.kts")
        ktlint()
    }
}

// The key the agent packages are signed with is the repository's own file; the console shows it (U1b, В5).
val releaseKey = rootProject.layout.projectDirectory.file("deploy/release/sard-release.pub")
tasks.processResources {
    from(releaseKey) { into("release") }
}
tasks.matching { it.name == "processMutatedMainResources" }.configureEach {
    (this as Copy).from(releaseKey) { into("release") }
}

mutflow {
    targets = listOf("dev.sard.server.**")
}

// mutflow's mutated copy of main also packages build-info.properties.
tasks.matching { it.name == "processMutatedMainResources" }.configureEach {
    dependsOn("bootBuildInfo")
}
