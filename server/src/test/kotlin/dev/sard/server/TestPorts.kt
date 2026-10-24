// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.net.InetAddress
import java.net.ServerSocket
import java.nio.channels.FileChannel
import java.nio.channels.FileLock
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.PosixFilePermissions
import java.util.concurrent.ConcurrentHashMap
import kotlin.random.Random

/** Below the range the OS hands out to outgoing connections (32768 and up on Linux). */
private const val FIRST = 20_000
private const val LAST = 30_000

/**
 * Ports a test binds by number, possibly more than once (a context restarted from the cache, a relay cut and
 * restored). A port the OS picked (port 0) is from the range of outgoing connections, and any client socket may take
 * it while it is not bound. A port that was free a moment ago is no better when several test JVMs run at once
 * (`maxParallelForks`): another one may bind it before this one does. So a port is taken for the life of the JVM:
 * in this JVM by [reserved], across JVMs by a lock on a file named after it, which the OS releases when the JVM ends.
 */
object TestPorts {
    private val reserved: MutableSet<Int> = ConcurrentHashMap.newKeySet()
    private val locks: MutableList<FileLock> = java.util.Collections.synchronizedList(mutableListOf())

    /** Only the owner's: the locks say nothing, but no other local user creates or removes them. */
    private val directory: Path by lazy {
        Files.createDirectories(
            Path.of(System.getProperty("java.io.tmpdir"), "sard-test-ports"),
            PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")),
        )
    }

    /** A loopback port no other test JVM holds and that nothing listens on now. */
    fun reserve(loopback: InetAddress = InetAddress.getLoopbackAddress()): Int {
        while (true) {
            val port = Random.nextInt(FIRST, LAST)
            if (reserved.add(port) && lock(port) && runCatching { ServerSocket(port, 1, loopback).close() }.isSuccess) {
                return port
            }
        }
    }

    private fun lock(port: Int): Boolean {
        val channel =
            FileChannel.open(directory.resolve("$port.lock"), StandardOpenOption.CREATE, StandardOpenOption.WRITE)
        val lock = runCatching { channel.tryLock() }.getOrNull()
        if (lock == null) {
            channel.close()
            return false
        }
        locks += lock
        return true
    }
}
