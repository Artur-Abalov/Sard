// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The extension sample is a build of its own: it compiles against the server-core that
// `./gradlew publishToMavenLocal` of the repository put into ~/.m2, as an enterprise build does.
pluginManagement {
    repositories {
        gradlePluginPortal()
        mavenCentral()
    }
}

dependencyResolutionManagement {
    repositoriesMode = RepositoriesMode.FAIL_ON_PROJECT_REPOS
    repositories {
        mavenLocal {
            content { includeGroup("dev.sard") }
        }
        mavenCentral()
    }
}

rootProject.name = "extension-sample"
