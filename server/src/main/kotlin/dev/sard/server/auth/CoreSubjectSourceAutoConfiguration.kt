// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.SubjectSource
import dev.sard.server.extension.SubjectSourceAutoConfiguration
import dev.sard.server.extension.singleImplementationOf
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean

/**
 * The [SubjectSource] of the core, unless an extension declared its own from an auto-configuration ordered
 * `before = [SubjectSourceAutoConfiguration::class]` (CORE1 Р26). It lives in `auth`, which knows the session; the
 * public `extension` package stays a leaf and only carries the seam and its check.
 */
@AutoConfiguration(after = [SubjectSourceAutoConfiguration::class])
class CoreSubjectSourceAutoConfiguration {
    companion object {
        /** Two extensions replacing the seam refuse the start (Р22, Р26). */
        @Bean
        @JvmStatic
        fun subjectSourceSeamCheck() = singleImplementationOf(SubjectSource::class.java)
    }

    @Bean
    @ConditionalOnMissingBean
    fun subjectSource(): SubjectSource = CoreSubjectSource()
}
