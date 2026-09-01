// openAuthWindow is the one way Studio opens an OAuth / sign-in page whose
// URL comes back from an API call.
//
// The trap it exists to close: `await api(); window.open(url)` works on
// desktop but is silently blocked on iOS Safari and the installed PWA,
// because iOS only honours window.open inside the SYNCHRONOUS user-gesture
// call stack. The boss tapped Connect on his iPhone and nothing happened.
//
// So the contract is: call openAuthWindow() synchronously in the tap
// handler, BEFORE any await. It claims a window while the gesture is still
// live, then navigate(url) points it at the provider once the API answers.
// Where even the synchronous open is refused (iOS standalone PWA does
// this), navigate() falls back to a same-context navigation - which iOS
// standalone renders as an in-app browser sheet with a Done button, so the
// app and its state survive underneath.
export type AuthWindow = {
  /** Point the claimed window at the sign-in URL (or fall back in-context). */
  navigate: (url: string) => void;
  /** Abandon the claimed window - call when the API errored. */
  close: () => void;
};

export function openAuthWindow(): AuthWindow {
  let w: Window | null = null;
  try {
    w = window.open("", "_blank");
  } catch {
    w = null;
  }
  if (w) {
    try {
      // The destination is a third-party auth page; it must not get a
      // handle back to Studio. We keep OUR handle to steer w.location.
      w.opener = null;
      w.document.write(
        '<title>Opening sign-in…</title><body style="font-family:system-ui;display:grid;place-items:center;height:100dvh;margin:0;color:#666">Opening sign-in…</body>',
      );
    } catch {
      // Never let placeholder chrome break the actual flow.
    }
  }
  return {
    navigate(url: string) {
      if (w && !w.closed) {
        try {
          w.location.href = url;
          return;
        } catch {
          // fall through to same-context navigation
        }
      }
      window.location.assign(url);
    },
    close() {
      try {
        w?.close();
      } catch {
        // already gone
      }
    },
  };
}
