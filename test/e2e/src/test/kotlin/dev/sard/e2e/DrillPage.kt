// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

/** The page of docs/operator/12-server-recovery.md cannot be run as written; the message names the block. */
internal class DrillPageError(
    message: String,
) : IllegalStateException(message)

/** A block of the page that the drill runs: [id] from the `<!-- drill: id -->` line, [text] as written. */
internal class DrillBlock(
    val id: String,
    val text: String,
)

/** A block with its substitutions made: what is run on the machine. */
internal class PreparedBlock(
    val id: String,
    val text: String,
)

/**
 * How the drill reads the recovery page (Р39): the blocks marked by `<!-- drill: id -->` right before a ```bash
 * block, in the order of the page, and the values in angle brackets (`<repositoryId>`) replaced from the drill's table.
 * A name that is not in the table, or an id used twice, stops the drill before anything is run.
 */
internal object DrillPage {
    private val MARKED = Regex("""(?m)^<!-- drill: ([\w-]+) -->\n```bash\n(.*?)\n```""", RegexOption.DOT_MATCHES_ALL)

    /** Any line that starts like a mark: a malformed one is an error, not a block that silently never runs. */
    private val MARK = Regex("""(?m)^<!--\s*drill:.*$""")

    /** A value in angle brackets: a name of letters, digits and `_.-`; shell redirections never look like it. */
    private val SUBSTITUTION = Regex("""<(\p{L}[\p{L}\p{N}_.-]*)>""")

    fun blocks(page: String): List<DrillBlock> {
        val blocks = MARKED.findAll(page).map { DrillBlock(it.groupValues[1], it.groupValues[2]) }.toList()
        val started = MARKED.findAll(page).map { it.range.first }.toSet()
        val bare = MARK.findAll(page).firstOrNull { it.range.first !in started }
        if (bare != null) throw DrillPageError("the mark \"${bare.value}\" is not a well-formed mark followed by a bash block")
        val repeated = blocks.groupBy { it.id }.filterValues { it.size > 1 }.keys
        if (repeated.isNotEmpty()) throw DrillPageError("the page marks block ${repeated.first()} for the drill more than once")
        return blocks
    }

    /** Every block with its values substituted; fails on the first name [values] lacks, before any block is run. */
    fun prepare(
        blocks: List<DrillBlock>,
        values: Map<String, String>,
    ): List<PreparedBlock> =
        blocks.map { block ->
            val text =
                SUBSTITUTION.replace(block.text) { match ->
                    values[match.groupValues[1]]
                        ?: throw DrillPageError("block ${block.id} has the substitution <${match.groupValues[1]}>, which the drill has no value for")
                }
            PreparedBlock(block.id, text)
        }

    /** Prepares all blocks, then hands them in turn to [execute] (its exit code); a nonzero code stops the run. */
    fun run(
        page: String,
        values: Map<String, String>,
        execute: (PreparedBlock) -> Int,
    ) {
        for (block in prepare(blocks(page), values)) {
            val code = execute(block)
            if (code != 0) throw DrillPageError("block ${block.id} exited with code $code")
        }
    }
}
