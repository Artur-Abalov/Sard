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
    `maven-publish`
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

// What an enterprise build and the extension sample compile against (CORE1 phase 3, Р1): the version is the
// build's X.Y.Z and the public API is dev.sard.server.extension.**. No remote repository yet; the release workflow adds it.
publishing {
    publications {
        create<MavenPublication>("maven") {
            from(components["java"])
            artifactId = "sard-server-console"
            // The versions the build resolved, not the bare coordinates of the BOM-managed ones.
            versionMapping {
                allVariants { fromResolutionResult() }
            }
            pom {
                name = "Sard web console"
                description = "The built web console, served by the sard-server application"
                url = "https://github.com/Artur-Abalov/Sard"
                licenses {
                    license {
                        name = "AGPL-3.0-only"
                        url = "https://spdx.org/licenses/AGPL-3.0-only.html"
                    }
                }
                scm { url = "https://github.com/Artur-Abalov/Sard" }
            }
        }
    }
}
