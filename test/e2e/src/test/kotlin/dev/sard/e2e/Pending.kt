// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

// Scenarios that wait for server features. Each class has no test methods on
// purpose: nothing is disabled or skipped (CLAUDE.md, rule 2), there is simply
// nothing to run yet. When the condition holds, add the tests here with a
// `SardEnvironment` extension, following the steps, and move the class into its
// own file. The list is mirrored in test/e2e/README.md.

/**
 * Transport + executor with the real server (plan, tail 5).
 *
 * Enable when: S7 (the server records progress and results) is in main. S6a
 * (steps sent over Connect) is, see [RunStepSeamTest].
 *
 * Steps:
 * 1. Enroll an agent ([AgentEnroller]) and start it as in [AgentConnectTest],
 *    with a local restic repository in the agent config (a volume, password
 *    file in the container).
 * 2. Make the server send one step to that agent (S8b REST, or rows as in [RunStepSeamTest]).
 * 3. Assert the agent acknowledges it, reports progress and a result on the
 *    stream, and the server stores the result (run/step rows).
 * 4. Restart the agent container mid-step: the executor state dir keeps the
 *    result and it is delivered after reconnect (A4 behaviour).
 */
class TransportExecutorPending

/**
 * T2, the full chain (test/e2e/README.md): token → enroll → workflow → restic
 * snapshot → verified restore → `lastVerifiedRestoreAt`.
 *
 * Enable when: S7 (runs driven by the server) is in main, and A2 so that
 * enrollment goes through `sard-agent enroll` ([AgentEnroller] replaced).
 *
 * Steps:
 * 1. Add a source PostgreSQL container and a local restic repository to the
 *    environment's network.
 * 2. Create a token ([EnrollmentTokens], by then the S8b REST call), enroll and
 *    start the agent.
 * 3. Start `examples/workflows/postgres-nightly.yaml` through the REST API.
 * 4. Assert a snapshot exists in the repository and no backup data passed
 *    through the server (server container has no repository access).
 * 5. Run restore verification; assert `/api/v1/status` reports a non-empty
 *    `lastVerifiedRestoreAt`.
 * The behaviour scenarios are written separately (out of T2a scope).
 */
class FullChainT2Pending

/**
 * T3, stream break: the Connect stream is cut while a run is in progress.
 *
 * Enable when: S7 is in main (a run to interrupt).
 *
 * Steps:
 * 1. Start a long step as in [FullChainT2Pending].
 * 2. Cut the agent off the network (`docker network disconnect`) for longer
 *    than the heartbeat interval, then reconnect it.
 * 3. Assert the server marks the agent offline and back online, the agent
 *    reconnects with backoff and sends Hello again, and the step's result is
 *    delivered exactly once.
 * The behaviour scenarios are written separately (out of T2a scope).
 */
class StreamBreakT3Pending
