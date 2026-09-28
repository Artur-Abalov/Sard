// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import kotlin.test.Test
import kotlin.test.assertFalse

@ExtendWith(OutputCaptureExtension::class)
class EnrollmentDispatcherConfigurationTest {
    @Test
    fun `closing the context leaves the shared IO dispatcher alone`(output: CapturedOutput) {
        ApplicationContextRunner()
            .withUserConfiguration(EnrollmentDispatcherConfiguration::class.java)
            .run { }

        assertFalse(
            output.toString().contains("Invocation of close method failed"),
            "closing the context must not try to close the shared Dispatchers.IO instance: $output",
        )
    }
}
