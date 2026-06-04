import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import vm from "node:vm";

const voiceHookPath = resolve(new URL("..", import.meta.url).pathname, "lib/voice/use-voice.ts");
const voiceClientPath = resolve(new URL("..", import.meta.url).pathname, "lib/voice/client.ts");
const chatHookPath = resolve(new URL("..", import.meta.url).pathname, "hooks/useChat.ts");
const source = readFileSync(voiceHookPath, "utf8");
const clientSource = readFileSync(voiceClientPath, "utf8");
const chatSource = readFileSync(chatHookPath, "utf8");
const match = source.match(/function preserveVoiceTranscript\(text: string\): string \{\n([\s\S]*?)\n\}/);

if (!match) {
  throw new Error("preserveVoiceTranscript() not found");
}

const js = `function preserveVoiceTranscript(text) {\n${match[1]}\n}; preserveVoiceTranscript;`;
const preserveVoiceTranscript = vm.runInNewContext(js);

const spoken = "  Wait — captions must match spoken output... exactly.  Multiple   spaces stay too.  ";
const caption = preserveVoiceTranscript(spoken);
const expected = "Wait — captions must match spoken output... exactly.  Multiple   spaces stay too.";

if (caption !== expected) {
  throw new Error(`caption mismatch\nexpected: ${JSON.stringify(expected)}\nactual:   ${JSON.stringify(caption)}`);
}

const oldNormalized = spoken
  .replace(/\s*[\u2013\u2014]\s*/g, ", ")
  .replace(/\s+/g, " ")
  .trim();

if (caption === oldNormalized) {
  throw new Error("caption still matches the old transcript-rewriting behavior");
}

console.log("voice transcript preservation test passed");
console.log(JSON.stringify({ spoken: spoken.trim(), caption }));


const requiredSnippets = [
  'onAssistantDelta?: (event: AssistantTranscriptEvent) => void',
  'voiceResponseId?: string',
  'voiceLastSequence?: number',
  'pending.voiceResponseId && pending.voiceResponseId !== event.responseId',
  '(pending.voiceLastSequence ?? 0) >= event.sequence',
  'text: event.isFinal ? (finalText || pending.text) : pending.text + text',
  'if (next[i].voiceResponseId === event.responseId) return next',
  'private finalizedResponses: Set<string> = new Set();',
  'this.finalizedResponses.has(respId)',
];

for (const snippet of requiredSnippets) {
  if (!chatSource.includes(snippet) && !source.includes(snippet) && !clientSource.includes(snippet)) {
    throw new Error(`missing voice transcript pipeline guard: ${snippet}`);
  }
}

const speechStartedCase = clientSource.match(/case "input_audio_buffer\.speech_started": \{([\s\S]*?)break;\n      \}/);
if (!speechStartedCase) {
  throw new Error('speech_started handler not found');
}
if (speechStartedCase[1].includes('emitAssistantTranscript') || speechStartedCase[1].includes('assistantBuf.clear')) {
  throw new Error('speech_started still finalizes or clears assistant transcript buffers');
}

function applyVoiceEvent(messages, event) {
  const text = event.text;
  const finalText = text.trim();
  if (!text && !event.isFinal) return messages;
  const next = [...messages];
  const pendingAssistantIdx = (() => {
    for (let i = next.length - 1; i >= 0; i--) {
      if (next[i].role === 'assistant' && next[i].pending) return i;
    }
    return -1;
  })();
  if (pendingAssistantIdx >= 0) {
    const pending = next[pendingAssistantIdx];
    if (pending.voiceResponseId && pending.voiceResponseId !== event.responseId) {
      next[pendingAssistantIdx] = { ...pending, pending: false };
    } else if ((pending.voiceLastSequence ?? 0) >= event.sequence) {
      return next;
    } else {
      next[pendingAssistantIdx] = {
        ...pending,
        text: event.isFinal ? (finalText || pending.text) : pending.text + text,
        pending: !event.isFinal,
        voiceResponseId: event.responseId,
        voiceLastSequence: event.sequence,
        voiceTranscriptSource: event.source,
      };
      return next;
    }
  }
  for (let i = next.length - 1, seen = 0; i >= 0 && seen < 8; i--) {
    if (next[i].role !== 'assistant') continue;
    seen++;
    if (next[i].voiceResponseId === event.responseId) return next;
  }
  next.push({
    role: 'assistant',
    text: event.isFinal ? finalText : text,
    pending: !event.isFinal,
    voiceResponseId: event.responseId,
    voiceLastSequence: event.sequence,
    voiceTranscriptSource: event.source,
  });
  return next;
}

let messages = [];
messages = applyVoiceEvent(messages, { text: 'Hello ', isFinal: false, responseId: 'resp_1', sequence: 1, source: 'output_audio_transcript' });
messages = applyVoiceEvent(messages, { text: 'boss', isFinal: false, responseId: 'resp_1', sequence: 2, source: 'output_audio_transcript' });
messages = applyVoiceEvent(messages, { text: 'Hello boss, final from audio.', isFinal: true, responseId: 'resp_1', sequence: 3, source: 'output_audio_transcript' });
if (messages.length !== 1 || messages[0].text !== 'Hello boss, final from audio.' || messages[0].pending) {
  throw new Error(`final audio transcript did not replace streamed partials: ${JSON.stringify(messages)}`);
}
messages = applyVoiceEvent(messages, { text: ' stale', isFinal: false, responseId: 'resp_1', sequence: 2, source: 'output_audio_transcript' });
if (messages.length !== 1 || messages[0].text !== 'Hello boss, final from audio.') {
  throw new Error('out-of-order duplicate delta mutated finalized transcript');
}
messages = applyVoiceEvent([{ role: 'assistant', text: 'first', pending: true, voiceResponseId: 'resp_a', voiceLastSequence: 1 }], { text: 'second', isFinal: false, responseId: 'resp_b', sequence: 1, source: 'output_audio_transcript' });
if (messages.length !== 2 || messages[0].pending || messages[1].voiceResponseId !== 'resp_b') {
  throw new Error(`new response id did not split assistant bubbles: ${JSON.stringify(messages)}`);
}

console.log('voice transcript source/ordering tests passed');
