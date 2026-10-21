// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

private val SAMPLE = Regex("""^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)})? (\S+)(?: -?\d+)?$""")
private val LABEL = Regex("""([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"""")

/** One line of the Prometheus text format: a series and its value. */
data class Sample(
    val name: String,
    val labels: Map<String, String>,
    val value: Double,
)

/** A scrape of the Prometheus text format 0.0.4, read the way a test asks about it. */
class Exposition(
    val text: String,
) {
    val samples: List<Sample> =
        text
            .lines()
            .filter { it.isNotBlank() && !it.startsWith("#") }
            .map { line ->
                val match = requireNotNull(SAMPLE.matchEntire(line)) { "not a sample line: $line" }
                val labels = LABEL.findAll(match.groupValues[2]).associate { it.groupValues[1] to it.groupValues[2] }
                Sample(match.groupValues[1], labels, number(match.groupValues[3]))
            }

    /** The series called [name] whose labels include [labels]. */
    fun series(
        name: String,
        vararg labels: Pair<String, String>,
    ): List<Sample> = samples.filter { it.name == name && labels.all { (k, v) -> it.labels[k] == v } }

    /** The value of the one series called [name] with [labels]; null when there is none. */
    fun value(
        name: String,
        vararg labels: Pair<String, String>,
    ): Double? {
        val found = series(name, *labels)
        check(found.size <= 1) { "$name${labels.toList()} is ${found.size} series" }
        return found.singleOrNull()?.value
    }

    fun has(name: String) = samples.any { it.name == name }

    /** The metric families the exposition declares with `# TYPE`. */
    fun families(): List<String> =
        text
            .lines()
            .filter { it.startsWith("# TYPE ") }
            .map { it.removePrefix("# TYPE ").substringBefore(' ') }

    private fun number(text: String): Double =
        when (text) {
            "+Inf", "Inf" -> Double.POSITIVE_INFINITY
            "-Inf" -> Double.NEGATIVE_INFINITY
            else -> text.toDouble()
        }
}
