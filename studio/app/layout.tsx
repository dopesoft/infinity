import type { Metadata, Viewport } from "next";
import { Mulish } from "next/font/google";
import { AuthProvider } from "@/lib/auth/session";
import { NavBadgesProvider } from "@/lib/nav-badges";
import { RealtimeProvider } from "@/lib/realtime/provider";
import { WebSocketProvider } from "@/lib/ws/provider";
import { OnboardingGate } from "@/components/OnboardingGate";
import { TrustToast } from "@/components/TrustToast";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { PWARegister } from "@/components/PWARegister";
import "./globals.css";

// Bust edge HTML cache on every request. Without this, Railway/Next caches the
// page HTML for ~1y while emitting new immutable chunk hashes on each deploy,
// so a stale browser sticks to dead chunks (root cause of the "thinking forever"
// bug after a redeploy). HTML must always reflect the latest chunk URLs.
export const dynamic = "force-dynamic";
export const revalidate = 0;

const mulish = Mulish({
  subsets: ["latin"],
  variable: "--font-mulish",
  display: "swap",
  weight: ["300", "400", "500", "600", "700", "800"],
});

export const metadata: Metadata = {
  title: "Infinity",
  description: "Single-user AI agent with persistent memory.",
  applicationName: "Infinity",
  // The web app manifest unlocks "Add to Home Screen" on iOS Safari (16.4+)
  // and "Install app" on every desktop browser. Apple still wants its own
  // apple-touch-icon link tag, which the `icons.apple` entry below emits.
  manifest: "/manifest.webmanifest",
  icons: {
    icon: [
      { url: "/icon-192.png", sizes: "192x192", type: "image/png" },
      { url: "/icon-512.png", sizes: "512x512", type: "image/png" },
      { url: "/icon.svg", type: "image/svg+xml" },
    ],
    apple: [{ url: "/apple-touch-icon.png", sizes: "180x180", type: "image/png" }],
  },
  appleWebApp: {
    capable: true,
    title: "Infinity",
    statusBarStyle: "default",
  },
  other: {
    "mobile-web-app-capable": "yes",
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 5,
  viewportFit: "cover",
  // resizes-content makes the layout viewport (and 100dvh math) shrink
  // when the iOS keyboard opens, keeping the sticky composer above the
  // keyboard without manual scroll-into-view.
  interactiveWidget: "resizes-content",
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
    { media: "(prefers-color-scheme: dark)", color: "#000000" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className={`${mulish.variable} font-sans`}>
        <AuthProvider>
          <RealtimeProvider>
            <NavBadgesProvider>
              <WebSocketProvider>
                <TooltipProvider delayDuration={250}>
                  <PWARegister />
                  <TrustToast />
                  <OnboardingGate>{children}</OnboardingGate>
                  <Toaster />

                </TooltipProvider>
              </WebSocketProvider>
            </NavBadgesProvider>
          </RealtimeProvider>
        </AuthProvider>
      </body>
    </html>
  );
}
