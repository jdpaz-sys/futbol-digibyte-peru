import type { Metadata } from "next";
import { getCommercialWhatsAppUrl } from "@/shared/site-config";
import { BridgeScreen } from "@/webapp/components/BridgeScreen";

export const metadata: Metadata = {
  title: "Acceso Dueños y Operadores",
  description:
    "Ingreso al panel operativo para dueños de complejos deportivos y administración.",
};

const LOGIN_FEATURES = [
  {
    code: "01",
    label: "Validación de comprobantes Yape/Plin en un clic con alertas en vivo",
  },
  {
    code: "02",
    label: "Agenda unificada sin dobles reservas ni cruces por WhatsApp",
  },
  {
    code: "03",
    label: "Control de check-in mediante ticket QR en puerta y reportes claros",
  },
] as const;

export default function LoginBridgePage() {
  return (
    <BridgeScreen
      badge="PANEL OPERATIVO · RUTA PUENTE LISTA"
      title="Acceso para dueños y complejos afiliados."
      description="Este módulo aislado (app) conectará la autenticación segura y la consola de administración de canchas en la Fase 4B."
      features={LOGIN_FEATURES}
      secondaryAction={{
        label: "Afiliar mi complejo",
        href: getCommercialWhatsAppUrl(),
        external: true,
      }}
    />
  );
}
