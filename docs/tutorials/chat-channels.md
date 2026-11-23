# Continue conversations in WhatsApp or Telegram

Link a chat channel to send prompts to NusaShell and receive replies from the same conversation workspace you use in the browser.

## Connect WhatsApp

1. Open **Settings → Channels**.
2. In the WhatsApp section, choose **Connect** to display a pairing QR code.
3. On your phone, open WhatsApp → **Linked devices → Link a device**, then scan the code.
4. Configure the allowed senders before using the channel.

## Connect Telegram

1. In Telegram, create a bot with [BotFather](https://t.me/BotFather) and copy its token.
2. In **Settings → Channels**, open Telegram and enter the token.
3. Configure allowed senders and groups, then connect the bot.

## Link a chat to a conversation

When a message arrives, NusaShell routes it to a conversation. Replies can return to the originating chat, and a conversation can mirror final replies to its linked chats. Routing is configured from the conversation's linked-chat controls.

The browser keeps the full conversation stream. WhatsApp and Telegram receive the final reply, and can receive approval notifications when configured.

## If the channel will not connect

- Check its status in **Settings → Channels**.
- For WhatsApp, keep the linked phone available during initial pairing and reconnect if the session expires.
- For Telegram, verify the bot token and that the bot has been started in the target direct chat or added to the group.
- If a sender cannot reach NusaShell, review the channel allowlist and blocked sender list.

Next: [schedule an automation](automations.md) or [inspect a conversation in the browser](browser.md).
