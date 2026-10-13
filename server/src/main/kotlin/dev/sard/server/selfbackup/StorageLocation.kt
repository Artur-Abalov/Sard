// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

private const val MAX_LOCATION = 1024

private const val KNOWN_SCHEMES = "s3|sftp|rest|b2|gs|azure|swift|rclone|local|https?"
private const val ANY_SCHEME = "[a-z][a-z0-9+.-]*"

/**
 * The scheme prefixes (restic's backend and the URL's own, in any case: s3:https://, rest:http://, sftp://), then
 * the authority up to the first slash: [user[:password]@]host. A word before a colon is a scheme only when it is a
 * known backend or is followed by "//"; otherwise it is the user name of user:password@host.
 */
private val AUTHORITY = Regex("^((?:(?:$KNOWN_SCHEMES):|$ANY_SCHEME:(?=//))*)/*([^/]*)", RegexOption.IGNORE_CASE)

/** user:password@ or an http(s) user@ right after "//", wherever in the text the address starts (quotes, a prefix). */
private val EMBEDDED_USERINFO = Regex("""//[^/@\s]*:[^/@\s]*@|https?://[^/@\s]+@""", RegexOption.IGNORE_CASE)
private val HTTP_SCHEME = Regex("https?:$", RegexOption.IGNORE_CASE)

/** A user name in an http(s) address is a token (OQ-203). */
private fun isHttpUser(
    schemes: String,
    userinfo: String,
): Boolean = userinfo.isNotEmpty() && HTTP_SCHEME.containsMatchIn(schemes)

/** A password in user:password@, or any user name in an http(s) address (OQ-203). */
private fun hasCredentials(location: String): Boolean {
    // The pattern matches every string, if only as an empty prefix.
    val (schemes, authority) = AUTHORITY.findAll(location).first().destructured
    val userinfo = authority.substringBeforeLast('@', missingDelimiterValue = "")
    return ':' in userinfo || isHttpUser(schemes, userinfo)
}

/** No user:password@ or token user@ anywhere in the text, and none after the scheme prefixes (see [hasCredentials]). */
private fun isCredentialFree(location: String): Boolean {
    if (EMBEDDED_USERINFO.containsMatchIn(location)) return false
    return !hasCredentials(location)
}

/** The address holds no credentials and no control character, and is not long (OQ-203). */
fun isSafeLocation(location: String): Boolean =
    location.isNotEmpty() &&
        location.length <= MAX_LOCATION &&
        location.none { it.isISOControl() } &&
        isCredentialFree(location)
