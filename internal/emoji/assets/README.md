# Emoji assets

Drop custom emoji image files here to have the bot upload them as **application
emojis** on startup (when `EMOJI_UPLOAD=true`). They are then usable in every
server the bot is in, no per-guild upload needed.

- **Filename = emoji name.** `iron_bar.png` becomes the emoji `iron_bar`,
  rendered in text as `<:iron_bar:1234567890>`. Reference it in code via the
  `emoji.Store` (`store.Format("iron_bar")`).
- **Allowed names:** 2–32 characters, letters/digits/underscore only
  (Discord's rule). An invalid filename fails startup loudly.
- **Allowed formats:** `.png`, `.gif` (animated), `.jpg`/`.jpeg`, `.webp`.
  Anything else in this folder (like this README) is ignored.
- **Size:** each image must be < 256 KB (Discord's limit).
- **Limit:** an application may hold up to 2000 emojis.

The startup sync is idempotent: an emoji whose name already exists on the
application is left as-is (never re-uploaded), so adding a new file only uploads
the new one. The bot always lists existing application emojis on start — even
with `EMOJI_UPLOAD=false` — so emojis added by hand in the Developer Portal are
picked up by name too.
