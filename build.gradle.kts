// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The plugins below are locked too (buildscript-gradle.lockfile, SC1).
buildscript {
    configurations.classpath {
        resolutionStrategy.activateDependencyLocking()
    }
}

// Plugin versions for all Gradle projects; each project applies what it needs.
plugins {
    kotlin("jvm") version "2.4.20" apply false
    kotlin("plugin.spring") version "2.4.20" apply false
    kotlin("plugin.jpa") version "2.4.20" apply false
    id("org.springframework.boot") version "4.1.1" apply false
    id("io.spring.dependency-management") version "1.1.7" apply false
    id("com.google.protobuf") version "0.10.0" apply false
    id("dev.detekt") version "2.0.0-alpha.6" apply false
    id("com.diffplug.spotless") version "8.10.3" apply false
    id("io.github.anschnapp.mutflow") version "1.5.0" apply false
}

extra.set("springBootVersion", "4.1.1")

// Dependency locking (SC1, ADR 00XX-draft-supply-chain): every configuration of every project resolves
// to the versions in its gradle.lockfile, and a configuration without a lock state fails the build;
// gradle/verification-metadata.xml checks the checksum of each artifact. Both are written by
// scripts/gradle-lock.sh.
allprojects {
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
}
