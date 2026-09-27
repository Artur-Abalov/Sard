// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

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
