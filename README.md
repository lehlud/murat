# murat

`murat` is a small terminal mail client with an encrypted local store.

It syncs mail over IMAPS or JMAP, stores messages locally under your XDG data directory, and provides both a fullscreen TUI and focused CLI commands for scripting.

![murat TUI showcase](showcase/Screenshot%202026-06-16%20at%2008.23.06.png)

## Features

- Encrypted local mail store using the same file locations as the Python `murat` client.
- IMAPS and JMAP sync with delta fetching for already-known remote messages.
- SMTPS and JMAP sending.
- Fullscreen terminal UI with list, preview, filters, account cycling, async sync/send, reply/reply-all/forward, spam/trash/unread, header view, attachment save/open/import.
- Compose flow through `$VISUAL` or `$EDITOR`.
- PGP support through `gpg`: decrypt/verify on open, encrypt/sign/self-encrypt/attach-public-key while composing.
- Address index built from all seen mail identities, exposed through `murat lsp` for editor completion in compose drafts.
- No third-party Go dependencies.

## Build

```sh
go build ./cmd/murat
```

The binary is written to `./murat` if you pass `-o murat`:

```sh
go build -o murat ./cmd/murat
```

## Storage

`murat` uses these paths by default:

- Config: `~/.config/murat`
- Data: `~/.local/share/murat`
- Key: `~/.local/share/murat/key.gpg`
- Mail index: `~/.local/share/murat/mail.enc.json`
- Accounts: `~/.local/share/murat/accounts.enc.json`
- Search index: `~/.local/share/murat/search.enc.json`
- Raw encrypted EML blobs: `~/.local/share/murat/eml/`

Show resolved paths:

```sh
murat paths
```

## Quick Start

Initialize the local key:

```sh
murat init --gpg-key you@example.com
```

Add an IMAP account:

```sh
murat account add-imap --email you@example.com
```

Or add a JMAP account:

```sh
murat account add-jmap --email you@example.com
```

Sync and open the TUI:

```sh
murat sync
murat tui
```

Running `murat` without arguments starts the TUI.

## CLI

Common commands:

```sh
murat init --gpg-key KEY
murat account list
murat account add-imap
murat account add-jmap
murat sync [--account ID_OR_EMAIL] [--limit N]
murat compose --to you@example.com
murat list
murat open MESSAGE_PREFIX
murat read MESSAGE_PREFIX
murat unread MESSAGE_PREFIX
murat save-attachments MESSAGE_PREFIX [DIR]
murat import-eml FILE_OR_DIR...
murat lsp
murat paths
murat version
```

`sync` fetches only new remote messages after the initial local remote-id index is known.

## TUI

Useful keys:

- `j` / `k`: move selection
- `enter`: open message
- `space`: message actions
- `f`: filters
- `/`: search
- `s`: sync
- `c`: compose
- `a`: cycle accounts
- `q`: back or quit

Message action menu:

- `r`: reply
- `R`: reply all
- `f`: forward
- `h`: toggle headers
- `a`: attachments
- `u`: mark unread
- `t`: move to trash
- `s`: toggle spam

## Compose

Compose opens `$VISUAL`, then `$EDITOR`, then `vi`.

Draft files use mail-like headers:

```text
From: you@example.com
To: someone@example.com
Cc:
Bcc:
Subject: Hello

Body text here.
```

You can edit `From:` to send from another configured account. Replies pick the best initial sender from the message account and recipient headers.

CLI compose examples:

```sh
murat compose --to someone@example.com
murat compose --from other@example.com --to someone@example.com
murat compose --to someone@example.com --pgp encrypt,sign
```

## PGP

PGP uses the local `gpg` command.

While confirming a draft in the TUI, press `g` to open the PGP submenu. Available options are hidden unless usable:

- `s`: sign, only if a secret key exists for `From:`
- `a`: attach public key, only if a secret key/public key exists for `From:`
- `e`: encrypt, only if all recipient public keys are known
- `E`: self-encrypt, only if encryption is available and a sender key exists

Opening mail detects inline PGP messages/signatures and runs `gpg --decrypt` for decrypt/verify status.

## Editor Completion

`murat` keeps a known-address index from every address it has seen in mail headers and sent drafts.

`murat lsp` starts a small stdio language server that completes known identities in compose draft headers:

- `To:`
- `Cc:`
- `Bcc:`

When launching `$EDITOR`, `murat` prepends the current executable to `PATH`, so editor LSP configs can use `murat lsp` reliably.

Example Helix config:

```toml
[language-server.murat]
command = "murat"
args = ["lsp"]

[[language]]
name = "murat-compose"
scope = "source.murat-compose"
file-types = [{ glob = "murat-compose-*.txt" }, { glob = "**/murat-compose-*.txt" }]
language-servers = ["murat"]
```

## Compatibility Notes

The Go client intentionally uses the Python `murat` data locations and encryption envelope so both clients can share a store.

Mail metadata is kept in encrypted JSON; raw EML files are encrypted separately, so previewing message metadata does not require decrypting every raw mail blob.

## Security Notes

- Local store encryption depends on your `gpg` key protection and local machine security.
- Account secrets are stored inside the encrypted account store.
- PGP operations are delegated to `gpg`; key trust and recipient availability follow your local GnuPG keyring.
