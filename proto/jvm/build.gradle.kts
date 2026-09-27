// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

// JVM bindings of the agent contract: Java messages, gRPC Java and Kotlin
// coroutine stubs, generated at build time from ../ and never committed.

import com.google.protobuf.gradle.id

plugins {
    kotlin("jvm")
    id("io.spring.dependency-management")
    id("com.google.protobuf")
}

val springBootVersion = rootProject.extra["springBootVersion"] as String

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
