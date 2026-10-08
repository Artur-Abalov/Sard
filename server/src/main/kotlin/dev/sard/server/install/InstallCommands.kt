// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.enrollment.AgentEndpoint
import dev.sard.server.enrollment.asServiceUser

private const val USER = "sard-agent"
private const val UNIT = "sard-agent.service"
private const val SUMS = "SHA256SUMS"
private const val SIGNATURE = "SHA256SUMS.minisig"
private const val LIB = "/usr/libexec/sard"
private const val CONFIG = "/etc/sard/agent.yaml"
private const val EXAMPLE = "/etc/sard/agent.example.yaml"
private const val EXAMPLE_ADDRESS = "sard.example.com:9090"
private const val FIRST_REPOSITORY = "main"
private const val SERVICE_USER_ADD =
    "useradd --system --no-create-home --home-dir /var/lib/sard-agent --shell /usr/sbin/nologin --user-group $USER"

/** What stands in `enroll` where the token goes; the token itself is never in these commands. */
const val TOKEN_PLACEHOLDER = "<TOKEN>"

/** The release file to install and how: [file] is its name in manifest.json, [signed] if SHA256SUMS.minisig exists. */
data class ReleasePackage(
    val file: String,
    val format: InstallFormat,
    val signed: Boolean,
    val fetch: FetchTool,
)

/**
 * Shell commands of the install and upgrade blocks (U1b), built from the address the packages are
 * fetched from ([downloads]), the address agents dial ([endpoint], as in the enroll command) and
 * the release key. Only the commands of a Debian- or RHEL-family host's base system; the layout of the archive
 * is the layout of the deb (В3), so the steps after the install are the same.
 */
class InstallCommands(
    private val downloads: DownloadsUrl,
    private val endpoint: AgentEndpoint,
    private val key: ReleaseKey,
) {
    fun install(pkg: ReleasePackage): List<InstallStep> =
        verified(pkg) +
            listOf(
                step(StepKind.INSTALL, installCommands(pkg)),
                step(StepKind.CONFIGURE, listOf(configure())),
                step(StepKind.ENROLL, listOf(enroll())),
                step(StepKind.REPO_INIT, listOf(asServiceUser("repo init --generate-password $FIRST_REPOSITORY"))),
                step(StepKind.START, listOf("sudo systemctl enable --now $UNIT")),
            )

    fun upgrade(pkg: ReleasePackage): List<InstallStep> =
        verified(pkg) +
            when (pkg.format) {
                InstallFormat.DEB -> {
                    listOf(step(StepKind.UPGRADE, listOf(dpkg(pkg))))
                }

                InstallFormat.RPM -> {
                    listOf(step(StepKind.UPGRADE, listOf(rpm(pkg))))
                }

                InstallFormat.TAR -> {
                    listOf(
                        step(StepKind.UPGRADE, ArchiveCommands.unpack(pkg) + ArchiveCommands.replaceBinaries(pkg)),
                        step(StepKind.RESTART, listOf("sudo systemctl try-restart $UNIT")),
                    )
                }
            }

    /** Download, the sum of the package and, in a signed release, the signature of the sums. */
    private fun verified(pkg: ReleasePackage): List<InstallStep> =
        listOf(
            step(StepKind.DOWNLOAD, fetches(pkg)),
            step(StepKind.CHECKSUM, listOf("grep '  ${pkg.file}\$' $SUMS | sha256sum -c -")),
        ) +
            if (pkg.signed) {
                listOf(
                    step(StepKind.SIGNATURE, listOf("minisign -Vm $SUMS -P ${key.publicKey}"), optional = true),
                )
            } else {
                emptyList()
            }

    private fun fetches(pkg: ReleasePackage): List<String> {
        val files = listOf(pkg.file, SUMS) + if (pkg.signed) listOf(SIGNATURE) else emptyList()
        // curl -f and wget's own exit status turn an answer 404 into a failure of the step.
        val command = if (pkg.fetch == FetchTool.CURL) "curl -fsSLO" else "wget -nv"
        return files.map { "$command ${downloads.file(it)}" }
    }

    private fun installCommands(pkg: ReleasePackage): List<String> =
        when (pkg.format) {
            InstallFormat.DEB -> listOf(dpkg(pkg))
            InstallFormat.RPM -> listOf(rpm(pkg))
            InstallFormat.TAR -> ArchiveCommands.unpack(pkg) + ArchiveCommands.layout(pkg)
        }

    private fun dpkg(pkg: ReleasePackage) = "sudo dpkg -i ${pkg.file}"

    /** `rpm -Uvh` of a local file needs no repositories, unlike `dnf install ./file` (ADR 0048). */
    private fun rpm(pkg: ReleasePackage) = "sudo rpm -Uvh ${pkg.file}"

    private fun enroll() = endpoint.enrollCommand(TOKEN_PLACEHOLDER)

    /** The example becomes agent.yaml only if there is none: an operator's file is never overwritten. */
    private fun configure(): String {
        val sed = "sed -i \"s|address: $EXAMPLE_ADDRESS|address: ${endpoint.address}|\" $CONFIG"
        return "sudo sh -c '[ -e $CONFIG ] || { cp $EXAMPLE $CONFIG && $sed; }'"
    }

    private fun step(
        kind: StepKind,
        commands: List<String>,
        optional: Boolean = false,
    ) = InstallStep(kind, commands, optional)
}

/** The layout of the deb's files and postinst (В3), made from the unpacked archive. */
private object ArchiveCommands {
    fun unpack(pkg: ReleasePackage) = listOf("tar -xzf ${pkg.file}")

    fun replaceBinaries(pkg: ReleasePackage) = listOf("sudo install -m 0755 ${binaries(directory(pkg))} $LIB/")

    /** What the deb's files and postinst give a host, from the archive unpacked next to it. */
    fun layout(pkg: ReleasePackage): List<String> {
        val dir = directory(pkg)
        return listOf(
            "sudo sh -c 'grep -q \"^$USER:\" /etc/passwd || $SERVICE_USER_ADD'",
            "sudo install -d -m 0755 $LIB",
            "sudo install -m 0755 ${binaries(dir)} $LIB/",
            "sudo cp -sf $LIB/sard-agent /usr/bin/sard-agent",
            "sudo install -m 0644 $dir/$UNIT /usr/lib/systemd/system/$UNIT",
            "sudo install -d -o root -g $USER -m 0750 /etc/sard",
            "sudo install -d -o $USER -g $USER -m 0700 /etc/sard/tls /etc/sard/secrets /var/cache/sard/restic",
            "sudo install -m 0644 $dir/agent.example.yaml $EXAMPLE",
            "sudo systemctl daemon-reload",
        )
    }

    /** The archive unpacks into a directory named like the file without its suffix. */
    private fun directory(pkg: ReleasePackage) = pkg.file.removeSuffix(".${pkg.format.manifestName}")

    private fun binaries(dir: String) = "$dir/sard-agent $dir/restic"
}
