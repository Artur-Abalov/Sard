// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.core.io.FileSystemResource
import org.springframework.core.io.Resource
import org.springframework.http.HttpHeaders
import org.springframework.http.MediaType
import org.springframework.web.servlet.resource.PathResourceResolver
import org.springframework.web.servlet.resource.ResourceHttpRequestHandler
import org.springframework.web.servlet.resource.ResourceResolver
import org.springframework.web.servlet.resource.ResourceResolverChain
import java.nio.file.Path

/**
 * Static files of one release: Range, HEAD, ETag (the file's SHA-256) and Last-Modified come from
 * [ResourceHttpRequestHandler]; only names in the [catalog] resolve, everything else is 404.
 */
class AgentPackagesHandler(
    dir: Path,
    private val catalog: AgentPackageCatalog,
) : ResourceHttpRequestHandler() {
    init {
        setLocations(listOf(FileSystemResource("$dir/")))
        setResourceResolvers(listOf(ReleaseFilesOnly(catalog), PathResourceResolver()))
        // Only catalog files resolve (ReleaseFilesOnly), so every served file has a sum.
        setEtagGenerator { catalog.etag(it.filename.orEmpty()) ?: error("${it.filename} is not in the release") }
        afterPropertiesSet()
    }

    override fun getMediaType(
        request: HttpServletRequest,
        resource: Resource,
    ): MediaType = AgentPackageHeaders.mediaType(resource.filename.orEmpty())

    override fun setHeaders(
        response: HttpServletResponse,
        resource: Resource,
        mediaType: MediaType?,
    ) {
        super.setHeaders(response, resource, mediaType)
        response.setHeader(HttpHeaders.CACHE_CONTROL, AgentPackageHeaders.cacheControl(resource.filename.orEmpty()))
        response.setHeader("X-Content-Type-Options", "nosniff")
    }

    private class ReleaseFilesOnly(
        private val catalog: AgentPackageCatalog,
    ) : ResourceResolver {
        override fun resolveResource(
            request: HttpServletRequest?,
            requestPath: String,
            locations: List<Resource>,
            chain: ResourceResolverChain,
        ): Resource? = chain.takeIf { requestPath in catalog.files }?.resolveResource(request, requestPath, locations)

        override fun resolveUrlPath(
            resourcePath: String,
            locations: List<Resource>,
            chain: ResourceResolverChain,
        ): String? = null // no URLs are generated for release files
    }
}
