// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// End-to-end tests: PostgreSQL, sard-server and sard-agent as containers built
// from the current code (`make e2e` builds the images, then runs :e2e:test).
// The tests see the system from outside, like an agent or an operator: they
// depend on the contract (proto-jvm), never on server code.

plugins {
    kotlin("jvm")
    id("io.spring.dependency-management")
}

val springBootVersion = rootProject.extra["springBootVersion"] as String

dependencyManagement {
    imports {
        mavenBom("org.springframework.boot:spring-boot-dependencies:$springBootVersion") {
            bomProperty("kotlin.version", "2.4.20")
        }
    }
}

kotlin {
    jvmToolchain(25)
}

dependencies {
    testImplementation(project(":proto-jvm"))
    testImplementation("io.grpc:grpc-netty")
    // The database is read (and the seam tests' run rows seeded) directly; tokens, sources and runs come from the REST API (S8b).
    testImplementation("org.postgresql:postgresql")
    testImplementation("org.testcontainers:testcontainers-junit-jupiter")
    testImplementation("org.testcontainers:testcontainers-postgresql")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit5")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

// Image tags and the expected versions come from `make e2e`; the defaults match
// a manual `make e2e-images` with VERSION unset.
val e2eProperties =
    mapOf(
        "e2e.serverImage" to "sard-server:e2e",
        "e2e.agentImage" to "sard-agent:e2e",
        "e2e.version" to "dev",
    )

tasks.test {
    useJUnitPlatform()
    // Containers are rebuilt from images every run; a cached result proves nothing.
    outputs.upToDateWhen { false }
    e2eProperties.forEach { (name, default) ->
        systemProperty(name, providers.gradleProperty(name).getOrElse(default))
    }
    // Logs of failed tests and starts; emptied first, so the artifact holds this run only.
    val logsDir = layout.buildDirectory.dir("e2e-logs")
    doFirst { delete(logsDir) }
    systemProperty("e2e.logsDir", logsDir.get().asFile.path)
    systemProperty(
        "e2e.resticVersionFile",
        rootProject.file("agent/internal/restic/restic-version").path,
    )
    testLogging {
        events("passed", "skipped", "failed")
        exceptionFormat = org.gradle.api.tasks.testing.logging.TestExceptionFormat.FULL
        // `make e2e` runs Gradle with -q: a failed test still names itself in the CI log.
        quiet {
            events("failed")
            exceptionFormat = org.gradle.api.tasks.testing.logging.TestExceptionFormat.FULL
        }
    }
}
