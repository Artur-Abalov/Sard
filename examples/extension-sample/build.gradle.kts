// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import org.springframework.boot.gradle.tasks.bundling.BootJar

buildscript {
    configurations.classpath {
        resolutionStrategy.activateDependencyLocking()
    }
}

plugins {
    kotlin("jvm") version "2.4.20"
    kotlin("plugin.spring") version "2.4.20"
    id("org.springframework.boot") version "4.1.2"
    id("io.spring.dependency-management") version "1.1.7"
}

group = "dev.sard.sample"
version = providers.gradleProperty("sardVersion").getOrElse("dev")

// The same locking as the repository's build (strict, every configuration).
dependencyLocking {
    lockAllConfigurations()
    lockMode = LockMode.STRICT
}

tasks.register("resolveAndLockAll") {
    description = "Resolves every resolvable configuration, so --write-locks records all of them."
    val resolvable = configurations.filter { it.isCanBeResolved }
    doFirst {
        require(gradle.startParameter.isWriteDependencyLocks) { "run with --write-locks" }
    }
    doLast { resolvable.forEach { it.resolve() } }
}

// Kotlin 2.4.20 overrides the 2.3.x managed by Spring Boot 4.1.2, as in the repository's build.
extra["kotlin.version"] = "2.4.20"

kotlin {
    jvmToolchain(25)
    compilerOptions {
        freeCompilerArgs.addAll("-Xjsr305=strict")
    }
}

val sardVersion: String = version.toString()

dependencies {
    // The open core: the library, and its public API is dev.sard.server.extension.** only.
    implementation("dev.sard:sard-server-core:$sardVersion")
    // What the starter itself uses; server-core brings its own at runtime only.
    implementation("org.springframework.boot:spring-boot-starter-webmvc")
    implementation("org.springframework.boot:spring-boot-starter-jdbc")
    // SardSecurityCustomizer.customize takes an HttpSecurity: the types of Spring Security are part of the API.
    implementation("org.springframework.boot:spring-boot-starter-security")
    runtimeOnly("dev.sard:sard-server-console:$sardVersion")

    testImplementation("org.springframework.boot:spring-boot-starter-webmvc-test")
    testImplementation("org.springframework.boot:spring-boot-testcontainers")
    testImplementation("org.testcontainers:testcontainers-junit-jupiter")
    testImplementation("org.testcontainers:testcontainers-postgresql")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit5")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

springBoot {
    // The main class and the application are the core's; this build adds a starter to it.
    mainClass = "dev.sard.server.SardServerApplicationKt"
    buildInfo()
}

tasks.named<BootJar>("bootJar") {
    archiveFileName = "sard-server-sample.jar"
}

tasks.withType<Test>().configureEach {
    useJUnitPlatform()
    maxHeapSize = "1g"
    // The PostgreSQL of the installation and the script that prepares it come from the repository.
    val repo = rootProject.layout.projectDirectory.dir("../..")
    systemProperty("sample.postgres-pin-file", repo.file("deploy/postgres.env").asFile.path)
    systemProperty("sample.bootstrap-sql", repo.file("deploy/db/bootstrap.sql").asFile.path)
    systemProperty("jdk.httpclient.allowRestrictedHeaders", "host")
    environment("SARD_AGENT_DOWNLOADS", "false")
}
