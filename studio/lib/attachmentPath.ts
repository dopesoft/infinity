/**
 * The raw-bytes route for an attachment id. A leaf with no React and no
 * fetch so the pure transcript module (lib/chat/transcript.ts) can build a
 * chip's URL without dragging the upload client into a node test.
 * `lib/attachments.ts` re-exports it, so consumers keep one import.
 */
export function attachmentRawPath(id: string): string {
  return `/api/attachments/${encodeURIComponent(id)}/raw`;
}
