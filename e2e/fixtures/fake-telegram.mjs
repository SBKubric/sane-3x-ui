// A stand-in for the Telegram Bot API for the e2e harness (#219): the
// `panel-tg` service of docker-compose.yml runs its bot against it
// (tgBotAPIServer), so a spec can see the bot's @username in an invite link
// and play a person pressing Start, without Telegram. Node's standard
// library only; `node fake-telegram.mjs` in the node image.
//
// The bot side, under /bot<token>/<method>:
//   - getMe answers @e2e_invite_bot;
//   - getUpdates hands out the queued updates past its offset, holding the
//     long poll up to a second while there are none;
//   - sendMessage, editMessageText, sendDocument and sendPhoto are recorded
//     and answered with a message; anything else with true.
// The spec's side, under /control:
//   - POST /control/updates queues an update (its update_id is assigned);
//   - GET /control/sent lists what the bot sent: {method, chat_id, text,
//     message_id, reply_markup}.
import http from 'node:http';

const PORT = Number(process.env.PORT || 8081);
const ME = { id: 4242000, is_bot: true, first_name: 'E2E', username: process.env.BOT_USERNAME || 'e2e_invite_bot' };

const updates = [];
const sent = [];
let nextUpdateId = 1;
let nextMessageId = 1;
let wake = [];

function answer(res, result, status = 200) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(status === 200 ? { ok: true, result } : { ok: false, error_code: status, description: String(result) }));
}

function body(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => resolve(Buffer.concat(chunks)));
  });
}

// params reads a Bot API call's parameters: JSON, a form, or — for files — the
// plain fields of a multipart body.
function params(req, raw) {
  const type = req.headers['content-type'] || '';
  const text = raw.toString('utf8');
  if (type.includes('application/json')) {
    try {
      return JSON.parse(text || '{}');
    } catch {
      return {};
    }
  }
  if (type.includes('application/x-www-form-urlencoded')) {
    return Object.fromEntries(new URLSearchParams(text));
  }
  const out = {};
  if (type.includes('multipart/form-data')) {
    for (const m of text.matchAll(/name="([^"]+)"\r\n\r\n([^\r]*)\r\n/g)) out[m[1]] = m[2];
  }
  return out;
}

function pending(offset) {
  while (updates.length && updates[0].update_id < offset) updates.shift();
  return updates.slice();
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://fake');
  const raw = await body(req);

  if (url.pathname === '/health') return answer(res, true);
  if (url.pathname === '/control/sent') return answer(res, sent);
  if (url.pathname === '/control/updates' && req.method === 'POST') {
    const update = JSON.parse(raw.toString('utf8'));
    update.update_id = nextUpdateId++;
    updates.push(update);
    wake.forEach((w) => w());
    wake = [];
    return answer(res, update);
  }

  const call = url.pathname.match(/^\/bot[^/]+\/(\w+)$/);
  if (!call) return answer(res, 'Not Found', 404);
  const method = call[1];
  const p = params(req, raw);
  switch (method) {
    case 'getMe':
      return answer(res, ME);
    case 'getUpdates': {
      const offset = Number(p.offset || 0);
      if (!pending(offset).length) {
        await new Promise((resolve) => {
          const timer = setTimeout(resolve, 1000);
          wake.push(() => {
            clearTimeout(timer);
            resolve();
          });
        });
      }
      return answer(res, pending(offset));
    }
    case 'sendMessage':
    case 'editMessageText':
    case 'sendDocument':
    case 'sendPhoto': {
      const messageId = Number(p.message_id) || nextMessageId++;
      sent.push({ method, chat_id: p.chat_id, text: p.text || '', message_id: messageId, reply_markup: p.reply_markup });
      const chatId = Number(p.chat_id);
      return answer(res, {
        message_id: messageId,
        date: Math.floor(Date.now() / 1000),
        chat: { id: Number.isFinite(chatId) ? chatId : 0, type: 'private' },
        text: p.text || '',
      });
    }
    default:
      sent.push({ method, chat_id: p.chat_id, text: p.text || '' });
      return answer(res, true);
  }
});

server.listen(PORT, () => console.log(`fake Telegram Bot API on :${PORT}`));
