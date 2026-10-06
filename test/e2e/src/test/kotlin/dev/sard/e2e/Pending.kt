// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

// Scenarios that wait for server features. Each class has no test methods on
// purpose: nothing is disabled or skipped (CLAUDE.md, rule 2), there is simply
// nothing to run yet. When the condition holds, add the tests here with a
// `SardEnvironment` extension, following the steps, and move the class into its
// own file. The list is mirrored in test/e2e/README.md.

/**
 * T3, stream break: the Connect stream is cut while a run is in progress.
 *
 * Enable when: T3 is taken up (S7 is in main; a files step runs, see [FullChainTest]).
 *
 * Steps:
 * 1. Start a long step of the `e2e-slow` plugin as in [SlowStreamTest] (its duration is size/rate,
 *    not the amount of data; T3s). To restart the server instead, use
 *    [SardEnvironment.recreateServer]: the CA and the database survive ([ServerRecreateTest]).
 * 2. Cut the agent off the network (`docker network disconnect`) for longer
 *    than the heartbeat interval, then reconnect it.
 * 3. Assert the server marks the agent offline and back online, the agent
 *    reconnects with backoff and sends Hello again, and the step's result is
 *    delivered exactly once.
 * 4. Restart the agent container mid-step instead of cutting the network: the executor state dir
 *    keeps the result and it is delivered after reconnect, exactly once (A4 behaviour; moved here
 *    from the former transport + executor stub, T2b).
 * The behaviour scenarios are written separately (out of T2a scope).
 */
class StreamBreakT3Pending
