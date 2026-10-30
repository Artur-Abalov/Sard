// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

// JVM bindings of the agent contract: Java messages, gRPC Java and Kotlin
// coroutine stubs, generated at build time from ../ and never committed.

import com.google.protobuf.gradle.id

plugins {
    kotlin("jvm")
    id("io.spring.dependency-management")
    id("com.google.protobuf")
    `maven-publish`
}

val springBootVersion = rootProject.extra["springBootVersion"] as String

group = "dev.sard"
version = providers.gradleProperty("sardVersion").getOrElse("dev")

// The proto sources are the parent directory; keep build output outside it.
layout.buildDirectory = rootProject.layout.buildDirectory.dir("proto-jvm")

dependencyManagement {
    imports {
        mavenBom("org.springframework.boot:spring-boot-dependencies:$springBootVersion") {
            // Same Kotlin as the compiler plugin (the BOM manages 2.3.x).
            bomProperty("kotlin.version", "2.4.20")
        }
    }
}

kotlin {
    jvmToolchain(25)
}

dependencies {
    api("io.grpc:grpc-protobuf")
    api("io.grpc:grpc-stub")
    api("io.grpc:grpc-kotlin-stub")
    api("com.google.protobuf:protobuf-java")
    api("org.jetbrains.kotlinx:kotlinx-coroutines-core")
    constraints {
        // gRPC still asks for 33.6.0; GHSA-xxph-c9ww-hj94 is fixed in 33.7.2. The constraint reaches
        // every project that depends on this one (server, e2e).
        api("com.google.guava:guava:33.7.2-jre")
    }
}

sourceSets {
    main {
        proto {
            srcDir("..")
            include("sard/**")
        }
    }
}

protobuf {
    protoc {
        artifact = "com.google.protobuf:protoc:${dependencyManagement.importedProperties["protobuf-java.version"]}"
    }
    // Generator versions follow the runtime versions managed by Spring Boot.
    val versions = dependencyManagement.importedProperties
    plugins {
        id("grpc") { artifact = "io.grpc:protoc-gen-grpc-java:${versions["grpc-java.version"]}" }
        id("grpckt") { artifact = "io.grpc:protoc-gen-grpc-kotlin:${versions["grpc-kotlin.version"]}:jdk8@jar" }
    }
    generateProtoTasks {
        all().forEach {
            it.plugins {
                id("grpc") { option("@generated=omit") }
                id("grpckt")
            }
        }
    }
}

// The code generators are native executables of the build machine's platform. Locking (scripts/gradle-lock.sh)
// resolves them for every platform a contributor builds on, so gradle/verification-metadata.xml checks them
// all and not only the one of the machine that wrote it (SC1).
tasks.named("resolveAndLockAll") {
    val versions = dependencyManagement.importedProperties
    val executables =
        listOf("com.google.protobuf:protoc:${versions["protobuf-java.version"]}", "io.grpc:protoc-gen-grpc-java:${versions["grpc-java.version"]}")
    val platforms = listOf("linux-x86_64", "linux-aarch_64", "osx-x86_64", "osx-aarch_64", "windows-x86_64")
    val all = executables.flatMap { exe -> platforms.map { dependencies.create("$exe:$it@exe") } }
    doLast { configurations.detachedConfiguration(*all.toTypedArray()).resolve() }
}

// What an enterprise build and the extension sample compile against (CORE1 phase 3, Р1): the version is the
// build's X.Y.Z and the public API is dev.sard.server.extension.**. No remote repository yet; the release workflow adds it.
publishing {
    publications {
        create<MavenPublication>("maven") {
            from(components["java"])
            artifactId = "sard-proto-jvm"
            // The versions the build resolved, not the bare coordinates of the BOM-managed ones.
            versionMapping {
                allVariants { fromResolutionResult() }
            }
            pom {
                name = "Sard agent protocol (JVM)"
                description = "Generated Java and Kotlin code of the server-agent gRPC contract"
                url = "https://github.com/Artur-Abalov/Sard"
                licenses {
                    license {
                        name = "Apache-2.0"
                        url = "https://spdx.org/licenses/Apache-2.0.html"
                    }
                }
                scm { url = "https://github.com/Artur-Abalov/Sard" }
            }
        }
    }
}
