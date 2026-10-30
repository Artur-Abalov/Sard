// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration

/**
 * The place an extension orders itself against to replace the [SubjectSource] of the core:
 * `@AutoConfiguration(before = [SubjectSourceAutoConfiguration::class])` with its own bean. The core's
 * implementation is declared after this configuration with `@ConditionalOnMissingBean`, next to the check that
 * refuses the start when two extensions replace it (CORE1 Р26).
 */
@AutoConfiguration
class SubjectSourceAutoConfiguration
