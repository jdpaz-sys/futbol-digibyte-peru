/**
 * Configuración central compartida entre el espacio (landing) y el espacio (app).
 * Edita COMMERCIAL_WHATSAPP_PHONE para actualizar el contacto B2B de dueños de complejos.
 */

export const COMMERCIAL_WHATSAPP_PHONE = "51999999999";

export const SITE_CONFIG = {
  name: "Fútbol Digibyte Perú",
  shortName: "Fútbol Digibyte",
  organization: "Digibyte Perú",
  regionLaunch: "Huacho y expandiéndose",
  description:
    "Separa tu cancha sintética en segundos, congela tu horario en tiempo real y llega directo a la puerta con tu ticket QR.",
  apiBaseUrl:
    process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1",
  routes: {
    home: "/",
    explore: "/explorar",
    login: "/login",
  },
  media: {
    logo: "/media/logo-digibyte-futbol.png",
    heroDesktopWebm: "/media/hero-cancha-desktop.webm",
    heroDesktopMp4: "/media/hero-cancha-desktop.mp4",
    heroDesktopPoster: "/media/hero-poster-desktop.webp",
    heroMobileWebm: "/media/hero-cancha-mobile.webm",
    heroMobileMp4: "/media/hero-cancha-mobile.mp4",
    heroMobilePoster: "/media/hero-poster-mobile.webp",
    ballVideo: "/media/balon-desarme.mp4",
    ballPoster: "/media/balon-poster.jpg",
  },
  commercialWhatsapp: {
    phone: COMMERCIAL_WHATSAPP_PHONE,
    defaultMessage:
      "Hola equipo de Fútbol Digibyte Perú, administro un complejo deportivo y deseo información para afiliar mis canchas.",
  },
} as const;

export function getCommercialWhatsAppUrl(customMessage?: string): string {
  const message =
    customMessage ?? SITE_CONFIG.commercialWhatsapp.defaultMessage;
  return `https://wa.me/${SITE_CONFIG.commercialWhatsapp.phone}?text=${encodeURIComponent(message)}`;
}
