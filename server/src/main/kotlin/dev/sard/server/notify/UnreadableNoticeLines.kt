// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

private const val MAX_PATHS_TEXT = 500
private const val PATH_CUT = "…"

/** The section of the docs on whose behalf the agent reads files; the link is to main, like hostcheck's (ВН9). */
internal const val PERMISSIONS_DOC =
    "https://github.com/Artur-Abalov/sard/blob/main/docs/plugins/files.md#от-чьего-имени-читаются-файлы"

/**
 * The reason of a backup whose snapshot lacks files restic could not read (PL7-2, Р11, Р12): the number in the
 * notice's language, the paths within one budget of [MAX_PATHS_TEXT] characters, what is left, and the hint about
 * rights.
 */
internal class UnreadableNoticeLines(
    private val words: Wording,
) {
    /** Paths that fit the budget, and whether some are missing or the last of them was cut. */
    private class Listed(
        val paths: List<String>,
        val cut: Boolean,
    )

    fun lines(backup: BackupSizes): List<List<Message.Part>> {
        val count = backup.unreadableCount ?: 0
        val listed = list(backup.unreadablePaths)
        return listOf(reasonLine(count)) +
            listed.paths.map { listOf(Message.Code(it)) } +
            moreLine(count - listed.paths.size, listed.paths.isNotEmpty()) +
            fullTextLine(listed.cut) +
            listOf(listOf(Message.Text("${words.permissionHint}$PERMISSIONS_DOC")))
    }

    private fun reasonLine(count: Long) = listOf(Message.Text("${words.reason}${words.unreadableFiles}$count"))

    /** "and 3 more": what the count says beyond the paths listed; nothing when no path is listed. */
    private fun moreLine(
        left: Long,
        listedAny: Boolean,
    ): List<List<Message.Part>> =
        if (left > 0 && listedAny) {
            listOf(listOf(Message.Text("${words.moreBefore}$left${words.moreAfter}")))
        } else {
            emptyList()
        }

    private fun fullTextLine(cut: Boolean): List<List<Message.Part>> =
        if (cut) listOf(listOf(Message.Text(words.fullText))) else emptyList()

    /** Whole paths while their sum stays within the budget; a first path over it is cut and marked. */
    private fun list(paths: List<String>): Listed {
        var used = 0
        val fitting = mutableListOf<String>()
        for (path in paths) {
            val length = path.codePointCount(0, path.length)
            if (used + length > MAX_PATHS_TEXT) break
            fitting += path
            used += length
        }
        val first = paths.firstOrNull()
        return if (fitting.isEmpty() && first != null) {
            Listed(listOf(first.substring(0, first.offsetByCodePoints(0, MAX_PATHS_TEXT)) + PATH_CUT), cut = true)
        } else {
            Listed(fitting, cut = fitting.size < paths.size)
        }
    }
}
