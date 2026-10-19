// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.fleet.CertificateState
import dev.sard.server.fleet.CurrentCertificate
import java.time.Instant

/** The certificate as the REST API shows it, judged by the server's clock at [now] (C1 phase 3, П33). */
internal fun CurrentCertificate.toApi(now: Instant) =
    AgentCertificate(
        notBefore = lifetime.notBefore,
        notAfter = lifetime.notAfter,
        daysLeft = lifetime.daysLeft(now).coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
        state = lifetime.state(now).toApi(),
        renewalIssuedAt = renewalIssuedAt,
    )

private fun CertificateState.toApi(): AgentCertificateState =
    when (this) {
        CertificateState.OK -> AgentCertificateState.OK
        CertificateState.RENEWAL_DUE -> AgentCertificateState.RENEWAL_DUE
        CertificateState.EXPIRING -> AgentCertificateState.EXPIRING
        CertificateState.EXPIRED -> AgentCertificateState.EXPIRED
    }
