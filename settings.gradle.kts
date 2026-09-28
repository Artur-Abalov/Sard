// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

pluginManagement {
    repositories {
        gradlePluginPortal()
        mavenCentral()
    }
}

dependencyResolutionManagement {
    repositoriesMode = RepositoriesMode.FAIL_ON_PROJECT_REPOS
    repositories {
        mavenCentral()
    }
}

rootProject.name = "sard"

include("server", "proto-jvm")
project(":proto-jvm").projectDir = file("proto/jvm")

// End-to-end tests run against built images (`make e2e`). Image builds copy only
// what they need, without test/, so the project is included only where it exists.
if (file("test/e2e/build.gradle.kts").exists()) {
    include("e2e")
    project(":e2e").projectDir = file("test/e2e")
}
