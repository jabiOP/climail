# climail privacy policy

Effective 27 November 2026.

climail is a terminal client for Gmail. It runs entirely on your own
computer. There is no climail server, and the developer never receives, sees
or stores any of your data.

## What climail accesses

When you log in, you grant climail the Gmail modify scope
(`https://www.googleapis.com/auth/gmail.modify`). climail uses it only to do
what you ask in the app: list and read your mail, labels and attachments;
search; send mail and drafts; and archive, label, star, mark read or unread,
and trash messages.

## Where your data goes

- **Between your computer and Google only.** climail talks directly to
  Google's Gmail and OAuth servers. It sends nothing to the developer or to
  any third party, and it has no analytics, telemetry, ads or tracking.
- **Local cache.** Messages, labels and the addresses you correspond with are
  cached in a database on your computer
  (`~/.local/share/climail/climail.db` by default), readable only by you, so
  the app is fast and works offline.
- **Attachments** are saved to disk only when you ask climail to save or open
  them.
- **Viewing a message in your browser** (`V`), climail writes it to a file
  readable only by you and opens it there, with its scripts blocked. Your
  browser then loads the pictures the message links to from wherever the
  sender put them, as any mail app showing pictures does, so the sender can
  tell the message was opened.
- **Login token.** Your OAuth token is stored in your system keychain (macOS
  Keychain, or the Secret Service on Linux). If no keychain is available, it
  is kept in a file readable only by you
  (`~/.local/share/climail/token.json`).

## Sharing

climail does not sell, share or transfer your data to anyone. Its use of
information received from Google APIs adheres to the
[Google API Services User Data Policy](https://developers.google.com/terms/api-services-user-data-policy),
including the Limited Use requirements.

## Deleting your data

- `climail auth logout ACCOUNT` deletes that account's stored token and its
  local mail cache.
- You can also revoke climail's access at any time at
  <https://myaccount.google.com/permissions>.

## Changes

If this policy changes, the new version will be posted at this address with a
new effective date.

## Contact

Questions: open an issue at <https://github.com/jabiOP/climail/issues>
