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
