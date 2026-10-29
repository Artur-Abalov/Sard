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

// server-core (CORE1): the library the open-core application (server-app) and an enterprise
// build package; the project keeps its path :server so the gates and the tests' paths stay put.
base {
    archivesName = "sard-server-core"
}

// Kotlin 2.4.20 overrides the 2.3.x managed by Spring Boot 4.1.2 (owner's decision, see session log).
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
    // The Prometheus text exposition of the metrics listener (H1, phase 3); the version comes from the Spring Boot BOM.
    implementation("io.micrometer:micrometer-registry-prometheus")
    implementation("org.springframework.boot:spring-boot-starter-data-jpa")
    implementation("org.springframework.boot:spring-boot-starter-flyway")
    implementation("org.springframework.boot:spring-boot-starter-grpc-server")
    // Rich gRPC status details (google.rpc.ErrorInfo): already on the classpath transitively
    // through spring-boot-starter-grpc-server; declared directly because EnrollmentStatus uses it.
    implementation("io.grpc:grpc-protobuf")
    implementation("org.flywaydb:flyway-database-postgresql")
    implementation("org.bouncycastle:bcpkix-jdk18on:1.86")
    // Argon2PasswordEncoder only (ADR F4a); nothing else of Spring Security is on the classpath.
    implementation("org.springframework.security:spring-security-crypto")
    implementation("com.google.api.grpc:proto-google-common-protos:2.64.1")
    implementation("org.springdoc:springdoc-openapi-starter-webmvc-api:3.1.1")
    implementation("com.networknt:json-schema-validator:3.0.8")
    implementation("tools.jackson.module:jackson-module-kotlin")
    implementation("org.jetbrains.kotlin:kotlin-reflect")
    implementation(project(":proto-jvm"))
    implementation("org.postgresql:postgresql")

    testImplementation("org.springframework.boot:spring-boot-starter-webmvc-test")
    testImplementation("org.springframework.boot:spring-boot-starter-grpc-server-test")
    testImplementation("org.springframework.boot:spring-boot-testcontainers")
    testImplementation("org.testcontainers:testcontainers-junit-jupiter")
    testImplementation("org.testcontainers:testcontainers-postgresql")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit5")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

// A library: sard-server.jar and the build-info.properties the server reads its version from
// belong to the application that packages it (server-app). The tests read the build info from
// the classpath as the server does, so they get their own copy.
tasks.bootJar {
    enabled = false
}
tasks.jar {
    archiveClassifier = ""
}
val testBuildInfo by tasks.registering(org.springframework.boot.gradle.tasks.buildinfo.BuildInfo::class) {
    destinationDir = layout.buildDirectory.dir("test-build-info")
}
sourceSets.test {
    resources.srcDir(layout.buildDirectory.dir("test-build-info"))
}
tasks.processTestResources {
    dependsOn(testBuildInfo)
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

// mutflow keeps its session in one static field per JVM, so a mutation test that starts a Spring
// context must not share a JVM with any other context's background threads (ADR 00XX, mutflow
// isolation). Those classes carry @Tag("mutflow-isolated"), MutationIsolationTest fails if one lacks it, and they run in isolatedTest, one JVM per class.
val isolatedTag = "mutflow-isolated"

// The mutation run of the gate (ADR 0006): mutflow compiles a mutated copy of main and tests that.
val mutationRun = providers.gradleProperty("mutflow.enabled").map(String::toBoolean).getOrElse(false)

// Both test tasks are configured alike: the properties, environment, heap and, through the mutflow
// plugin (which configures every Test task), the mutated classes first on the classpath.
tasks.withType<Test>().configureEach {
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
    // The PostgreSQL of an installation (DB1): the test databases use its image and initdb arguments.
    systemProperty("sard.test.postgres-pin-file", rootProject.file("deploy/postgres.env").path)
    // The script that prepares a cluster for Sard (DB2): the tests run it as db-init and a DBA do.
    systemProperty("sard.test.bootstrap-sql", rootProject.file("deploy/db/bootstrap.sql").path)
    inputs.file(rootProject.file("deploy/db/bootstrap.sql"))
    // Every other image the tests run, pinned by digest (SC1).
    systemProperty("sard.test.images-file", rootProject.file("test/images.env").path)
    inputs.file(rootProject.file("test/images.env"))
    // The release order of versions, shared with the dpkg and rpm checks (ADR 0048).
    systemProperty("sard.test.version-order-file", rootProject.file("deploy/release/version-order.txt").path)
    // Tags that are not releases, shared with scripts/test-release-version.sh (ADR 0048).
    systemProperty("sard.test.not-release-tags-file", rootProject.file("deploy/release/not-release-tags.txt").path)
    // The CA directories of integration tests: every context gets a new one in here (FreshPkiDirectory), because
    // a CA directory goes with its database (F4a); the default /var/lib/sard/pki is not writable here.
    systemProperty(
        "sard.test.pki-base",
        layout.buildDirectory
            .dir("test-pki")
            .get()
            .asFile.path,
    )
    // CSRF tests (W1b) set the Host header explicitly to control the expected Origin;
    // java.net.http.HttpClient refuses to set it unless this is allowed.
    systemProperty("jdk.httpclient.allowRestrictedHeaders", "host")
    // Every cached Spring context keeps its server and pools alive; with the onboarding contexts the
    // mutation run (gate.sh, mutflow) outgrew the default 512 MiB worker heap and later contexts failed
    // to load. Capping the context cache instead changed which tests see background mutants, so the
    // cache stays as it is and the heap grows.
    maxHeapSize = "1g"
    // Two test JVMs at a time, each with its own Spring context cache and PostgreSQL container; one mutflow
    // session per JVM, so the isolation of ADR 00XX (mutflow isolation) holds in each.
    maxParallelForks = 2
    // No agent release next to the test server; AgentDownloadsIntegrationTest switches downloads on.
    environment("SARD_AGENT_DOWNLOADS", "false")
    // The mutation run (gate.sh, -Pmutflow.enabled=true) runs the mutated compilation, whose
    // coverage is meaningless and would overwrite the report of :server:check: no JaCoCo agent there.
    if (mutationRun) {
        extensions.configure<JacocoTaskExtension> { isEnabled = false }
    } else {
        finalizedBy(tasks.jacocoTestReport)
    }
}

// A class file that names text: how the test tasks pick classes by annotation or tag without loading them.
fun FileTreeElement.isClassNaming(text: String) =
    isDirectory || (name.endsWith(".class") && String(file.readBytes(), Charsets.ISO_8859_1).contains(text))

tasks.test {
    useJUnitPlatform { excludeTags(isolatedTag) }
    // Only @MutFlowTest classes kill mutants; the others already ran in :server:check.
    // The annotation descriptor, not the class name: MutationIsolationTest only refers to the class.
    if (mutationRun) include { it.isClassNaming("Lio/github/anschnapp/mutflow/junit/MutFlowTest;") }
}

val isolatedTest by tasks.registering(Test::class) {
    description = "Runs the mutation tests that start a Spring context, one JVM per class."
    group = LifecycleBasePlugin.VERIFICATION_GROUP
    testClassesDirs =
        sourceSets.test
            .get()
            .output.classesDirs
    classpath = sourceSets.test.get().runtimeClasspath
    useJUnitPlatform { includeTags(isolatedTag) }
    forkEvery = 1
    // forkEvery = 1 forks once per scanned class, and the tag filter works inside the fork; pick the
    // class files that name the tag so only the isolated classes (and no other) cost a JVM.
    include { it.isClassNaming(isolatedTag) }
    mustRunAfter(tasks.test)
}

jacoco {
    toolVersion = "0.8.15"
}

val jacocoData = layout.buildDirectory.files("jacoco/test.exec", "jacoco/isolatedTest.exec")

tasks.jacocoTestReport {
    // One report over both tasks' data, by file: Task-typed executionData would make each task a
    // dependency, and a run of one of them (a --tests filter) would pull in the other. A task that did
    // not run has no file; the report skips missing ones.
    mustRunAfter(tasks.test, isolatedTest)
    executionData(jacocoData)
    reports {
        xml.required = true
        html.required = false
    }
}

tasks.jacocoTestCoverageVerification {
    dependsOn(tasks.jacocoTestReport)
    executionData(jacocoData)
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
    dependsOn(tasks.jacocoTestCoverageVerification, isolatedTest)
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

