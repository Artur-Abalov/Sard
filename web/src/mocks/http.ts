// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createOpenApiHttp } from 'openapi-msw'
import type { paths } from '../api/schema'

// MSW's http bound to the generated OpenAPI paths: a handler for a path, status
// or body the schema does not declare is a compile error.
export const http = createOpenApiHttp<paths>()
