import type { Metadata } from "next";
import { SITE_CONFIG } from "@/shared/site-config";
import { BridgeScreen } from "@/webapp/components/BridgeScreen";

export const metadata: Metadata = {
  title: "Explorar Canchas",
  description:
    "Catálogo geolocalizado de complejos sintéticos y disponibilidad horaria en vivo.",
};

const EXPLORE_FEATURES = [
  {
    code: "01",
    label: "Complejos cercanos con precios y horarios reales sin registro previo",
  },
  {
    code: "02",
    label: "Congelamiento temporal de cancha con protección anticolisión",
  },
  {
    code: "03",
    label: "Pago directo Yape/Plin al complejo y ticket QR de ingreso",
  },
] as const;

export default function ExplorarBridgePage() {
  return (
    <BridgeScreen
      badge="APP WEB JUGADOR · RUTA PUENTE LISTA"
      title="Explorador de canchas en tiempo real."
      description="Este espacio independiente (app) alojará el mapa interactivo, la grilla horaria en vivo y el flujo de reserva rápida Mobile-First en la Fase 4B."
      features={EXPLORE_FEATURES}
      secondaryAction={{
        label: "Soy dueño de cancha",
        href: SITE_CONFIG.routes.login,
      }}
    />
  );
}
