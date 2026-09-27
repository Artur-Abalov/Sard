// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

plugins {
    kotlin("jvm")
    kotlin("plugin.spring")
    kotlin("plugin.jpa")
    id("org.springframework.boot")
    id("io.spring.dependency-management")
    id("dev.detekt")
    id("com.diffplug.spotless")
    id("io.github.anschnapp.mutflow")
    jacoco
}

group = "dev.sard"
version = providers.gradleProperty("sardVersion").getOrElse("dev")

// Kotlin 2.4.20 overrides the 2.3.x managed by Spring Boot 4.1.1 (owner's decision, see session log).
extra["kotlin.version"] = "2.4.20"

kotlin {
    jvmToolchain(25)
    compilerOptions {
        freeCompilerArgs.addAll("-Xjsr305=strict")
    }
}

dependencies {
    implementation("org.springframework.boot:spring-boot-starter-webmvc")
    implementation("org.springframework.boot:spring-boot-starter-actuator")
    implementation("org.springframework.boot:spring-boot-starter-data-jpa")
    implementation("org.springframework.boot:spring-boot-starter-flyway")
    implementation("org.springframework.boot:spring-boot-starter-grpc-server")
    implementation("org.flywaydb:flyway-database-postgresql")
    implementation("org.bouncycastle:bcpkix-jdk18on:1.86")
    implementation("org.springdoc:springdoc-openapi-starter-webmvc-api:3.1.1")
    implementation("tools.jackson.module:jackson-module-kotlin")
    implementation("org.jetbrains.kotlin:kotlin-reflect")
    implementation(project(":proto-jvm"))
    runtimeOnly("org.postgresql:postgresql")

    testImplementation("org.springframework.boot:spring-boot-starter-webmvc-test")
    testImplementation("org.springframework.boot:spring-boot-starter-grpc-server-test")
    testImplementation("org.springframework.boot:spring-boot-testcontainers")
    testImplementation("org.testcontainers:testcontainers-junit-jupiter")
    testImplementation("org.testcontainers:testcontainers-postgresql")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit5")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

springBoot {
    buildInfo()
}

tasks.bootJar {
    archiveFileName = "sard-server.jar"
}

tasks.test {
    useJUnitPlatform()
    // The CA of integration tests; the default /var/lib/sard/pki is not writable here.
    systemProperty(
        "sard.pki.dir",
        layout.buildDirectory
            .dir("test-pki")
            .get()
            .asFile.path,
    )
    finalizedBy(tasks.jacocoTestReport)
}

jacoco {
    toolVersion = "0.8.15"
}

tasks.jacocoTestReport {
    dependsOn(tasks.test)
    reports {
        xml.required = true
        html.required = false
    }
}

tasks.jacocoTestCoverageVerification {
    dependsOn(tasks.jacocoTestReport)
    violationRules {
        rule {
            limit {
                counter = "INSTRUCTION"
                minimum = "0.80".toBigDecimal()
            }
        }
    }
}

tasks.check {
    dependsOn(tasks.jacocoTestCoverageVerification)
}

// detekt runs on its own embedded compiler (built with Kotlin 2.4.10); keep the
// project's Kotlin 2.4.20 from leaking into its classpath.
configurations.matching { it.name == "detekt" }.configureEach {
    resolutionStrategy.eachDependency {
        if (requested.group == "org.jetbrains.kotlin") useVersion("2.4.10")
    }
}

detekt {
    buildUponDefaultConfig = true
    config.setFrom(files("config/detekt.yml"))
    source.setFrom("src/main/kotlin", "src/test/kotlin")
}

spotless {
    kotlin {
        target("src/**/*.kt")
        ktlint()
    }
    kotlinGradle {
        target("*.gradle.kts")
        ktlint()
    }
}

mutflow {
    targets = listOf("dev.sard.server.**")
}

// mutflow's mutated copy of main also packages build-info.properties.
tasks.matching { it.name == "processMutatedMainResources" }.configureEach {
    dependsOn("bootBuildInfo")
}
