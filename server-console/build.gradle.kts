// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The console (S10, ADR 0040) as its own jar: web/dist under console/, so the core
// application and an enterprise build package the same bundle next to server-core.
// The jar carries the console only when -PsardConsoleDist=<dir> names a finished build
// (make build and the image's Dockerfile do); without it the jar is empty and needs no
// Node. A directory without index.html fails the build, naming the property and the
// directory.

plugins {
    java
}

group = "dev.sard"
version = providers.gradleProperty("sardVersion").getOrElse("dev")

base {
    archivesName = "sard-server-console"
}

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
