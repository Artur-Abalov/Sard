// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The open-core application: no code of its own, only the packaging of server-core and
// the console into sard-server.jar. An enterprise build is the same thing with its
// starters added (CORE1); main and @SpringBootApplication live in server-core.

plugins {
    java
    id("org.springframework.boot")
    id("io.spring.dependency-management")
}

group = "dev.sard"
version = providers.gradleProperty("sardVersion").getOrElse("dev")

// Same Kotlin as server-core (the Spring Boot BOM manages 2.3.x).
extra["kotlin.version"] = "2.4.20"

java {
    toolchain {
        languageVersion = JavaLanguageVersion.of(25)
    }
}

dependencies {
    implementation(project(":server"))
    runtimeOnly(project(":server-console"))
}

springBoot {
    mainClass = "dev.sard.server.SardServerApplicationKt"
    // The coordinates the server reports (/api/v1/status, /actuator/info) stay those of
    // the single-module build.
    buildInfo {
        properties {
            artifact = "server"
            name = "server"
        }
    }
}

tasks.bootJar {
    archiveFileName = "sard-server.jar"
    manifest {
        attributes("Implementation-Title" to "server")
    }
}

// Nothing to run but the packaging: the plain jar of this project would be empty.
tasks.jar {
    enabled = false
}
