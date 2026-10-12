// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The only checks the console makes on password forms: a field is not empty and the
// confirmation repeats the password. Whether the password is long enough is the
// server's to say (422): the rules are shown as text only.

export function canSetPassword(password: string, confirmation: string): boolean {
  return password !== '' && password === confirmation
}

/** The hint under the confirmation: shown only once something was typed there. */
export function confirmationMismatch(password: string, confirmation: string): boolean {
  return confirmation !== '' && password !== confirmation
}

export function canChangePassword(
  current: string,
  password: string,
  confirmation: string,
): boolean {
  return current !== '' && canSetPassword(password, confirmation)
}
