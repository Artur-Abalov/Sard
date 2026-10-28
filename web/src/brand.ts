// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { productName } from './product'

// What the header of the console shows as the name of the product.
export function shellBrand(): string {
  return productName
}
