// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

/**
 * Marks a declaration that has to be public for the framework (the `@Bean` methods of the core's
 * auto-configurations) but is not part of the API of server-core: it is left out of the ABI dump
 * (`server/api/server.api`) and an extension must not call it. Only the class names of the `*AutoConfiguration`
 * classes are API, because an extension orders itself with `before = [...]` against them.
 */
@Retention(AnnotationRetention.BINARY)
@Target(AnnotationTarget.FUNCTION, AnnotationTarget.PROPERTY_GETTER)
annotation class InternalSardApi
