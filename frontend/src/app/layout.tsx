import type { Metadata, Viewport } from "next";
import {
  Bricolage_Grotesque,
  JetBrains_Mono,
  Manrope,
} from "next/font/google";
import { SITE_CONFIG } from "@/shared/site-config";
import "./globals.css";

const bricolageDisplay = Bricolage_Grotesque({
  subsets: ["latin", "latin-ext"],
  weight: ["500", "600", "700", "800"],
  variable: "--font-display",
  display: "swap",
});

const manropeSans = Manrope({
  subsets: ["latin", "latin-ext"],
  weight: ["400", "500", "600", "700"],
  variable: "--font-sans",
  display: "swap",
});

const jetbrainsMono = JetBrains_Mono({
  subsets: ["latin", "latin-ext"],
  weight: ["400", "500"],
  variable: "--font-mono",
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL(
    process.env.NEXT_PUBLIC_SITE_URL ?? "http://localhost:3000"
  ),
  title: {
    default: `${SITE_CONFIG.name} — Reserva canchas sintéticas en segundos`,
    template: `%s · ${SITE_CONFIG.name}`,
  },
  description: SITE_CONFIG.description,
  applicationName: SITE_CONFIG.name,
  manifest: "/manifest.json",
  icons: {
    icon: [
      {
        url: SITE_CONFIG.media.logo,
        type: "image/png",
        sizes: "256x256",
      },
    ],
    shortcut: SITE_CONFIG.media.logo,
    apple: SITE_CONFIG.media.logo,
  },
  openGraph: {
    title: `${SITE_CONFIG.name} — Separa tu cancha sin esperar por WhatsApp`,
    description: SITE_CONFIG.description,
    siteName: SITE_CONFIG.name,
    locale: "es_PE",
    type: "website",
    images: [
      {
        url: SITE_CONFIG.media.heroDesktopPoster,
        width: 1920,
        height: 1080,
        alt: SITE_CONFIG.name,
      },
    ],
  },
};

export const viewport: Viewport = {
  themeColor: "#070B14",
  width: "device-width",
  initialScale: 1,
};

const fontVariables = `${bricolageDisplay.variable} ${manropeSans.variable} ${jetbrainsMono.variable}`;

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="es-PE" className={fontVariables}>
      <body className={fontVariables}>{children}</body>
    </html>
  );
}
