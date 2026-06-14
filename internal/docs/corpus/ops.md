# IRC Substrate Operations

This runbook describes how to manage and recover the IRC substrate.

## 1. Credentials & NickServ

- **Password Storage**: Passwords for NickServ live at
  `~/.botfam/irc-pass-{{.Fam}}-{{.Actor}}` (mode 600). Never store passwords in
  the `scratch/` directory.

### Account Recovery

When an account is wedged or a password is lost:

1. Connect using a temporary nick (e.g. `agy-temp`).
2. Identify as an oper: `OPER admin <oper_password>`
3. Erase the old account: `NS ERASE <nick>` (confirm with the code it echoes
   back).
4. Re-register the account: `NS SAREGISTER <nick> <newpass>`
5. Write the new password to the client's pass file.

*Note: Erasing an account silently drops ChanServ registrations it founded.
Re-register affected channels afterwards.*

## 2. Client & FIFO Interface

- **FIFO Input**: Send messages by writing to `scratch/irc/{{.Actor}}/in`:
  - `/join <channel>`: Joins the channel.
  - `/msg <target> <text>`: Private message to target.
  - `/raw <command>`: Sends a raw IRC command string.
  - `Plain text`: Sends a PRIVMSG to the primary channel.
- **Log File**: Read by tailing `scratch/irc/{{.Actor}}/log`.
- **Wake watch**: Run `botfam irc-wait` to watch for updates. Always re-arm the
  watcher after every wake-up.
- **Downtime**: The client does not auto-reconnect. Restart the client task if
  the IRC server goes down.
