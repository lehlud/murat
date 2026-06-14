# murat

Go rewrite of `murat`.

Module: `lehnert.dev/murat`

Current scope:

- Fast AES-GCM encrypted index/body/raw stores.
- Message object owns local state mutations: `SetRead`, `SetSpam`, `MarkTrashed`.
- Separate encrypted body blobs, so opening mail does not decrypt large raw EML attachments.
- Uses Python murat locations: `~/.config/murat` and `~/.local/share/murat`.
- Uses Python murat `key.gpg`, `accounts.enc.json`, `mail.enc.json`, `search.enc.json`, and `eml/` paths directly.
- Writes the same Python ChaCha20/HMAC encrypted JSON envelope, so Python and Go clients share one store.
- Fullscreen TUI: list/preview/status bar, filters, account cycle, async sync/send, reply/reply-all/forward, spam/trash/unread, header toggle, attachment save.
- CLI: `init`, `account`, `sync`, `compose`, `import-eml`, `list`, `open`, `save-attachments`, `read`, `unread`, `tui`, `lsp`, `paths`, `version`.
- Protocols: IMAPS sync, SMTPS send, JMAP sync, JMAP send.
- PGP: inline decrypt/verify on open; compose supports `Pgp: encrypt`, `Pgp: sign`, or `Pgp: encrypt,sign` using `gpg` and account email as signing identity.

Build:

```sh
go build ./cmd/murat
```

Quick start:

```sh
murat init
murat import-eml ~/Mail/example.eml
murat list
murat open <message-prefix>
murat save-attachments <message-prefix> ~/Downloads
```

Compose opens `$VISUAL`/`$EDITOR` when run from a terminal:

```sh
murat compose --to you@example.com
murat compose --to you@example.com --pgp encrypt,sign
```

Compose draft files include `From:`, `To:`, `Cc:`, `Bcc:`, and `Subject:` headers. `murat lsp` runs a small stdio language server for address completion in `To:`, `Cc:`, and `Bcc:`.
