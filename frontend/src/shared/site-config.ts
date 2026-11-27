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
    "Separa tu cancha sintética en segundos, paga tu adelanto con Yape o Plin y entra directo a jugar con tu pase QR.",
  apiBaseUrl:
    process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1",
  routes: {
    home: "/",
    explore: "/explorar",
    login: "/login",
    ownersAnchor: "#duenos",
  },
  media: {
    logo: "/media/logo-digibyte-futbol.png",
    heroDesktopMp4: "/media/hero-cancha-desktop-final.mp4",
    heroDesktopPoster: "/media/hero-poster-desktop-final.webp",
    heroMobileMp4: "/media/hero-cancha-mobile-final.mp4",
    heroMobilePoster: "/media/hero-poster-mobile-final.webp",
    ballDesktopMp4: "/media/balon-desarme-desktop-final.mp4",
    ballDesktopPoster: "/media/balon-poster-desktop-final.webp",
    ballMobileMp4: "/media/balon-desarme-mobile-final.mp4",
    ballMobilePoster: "/media/balon-poster-mobile-final.webp",
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
