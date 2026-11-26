import Image from "next/image";
import Link from "next/link";
import { SITE_CONFIG } from "@/shared/site-config";

interface BridgeFeature {
  code: string;
  label: string;
}

interface BridgeSecondaryAction {
  label: string;
  href: string;
  external?: boolean;
}

interface BridgeScreenProps {
  badge: string;
  title: string;
  description: string;
  features: readonly BridgeFeature[];
  secondaryAction?: BridgeSecondaryAction;
}

export function BridgeScreen({
  badge,
  title,
  description,
  features,
  secondaryAction,
}: BridgeScreenProps) {
  return (
    <div className="flex min-h-dvh flex-col justify-between bg-[#F8FAFC] text-[#0F172A]">
      {/* Barra superior minimalista App */}
      <header className="border-b border-[#E2E8F0] bg-[#FFFFFF]/90 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-5xl items-center justify-between px-5 sm:px-8">
          <Link
            href={SITE_CONFIG.routes.home}
            className="flex items-center gap-2.5 rounded-full focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#2563EB]"
          >
            <span className="relative flex h-8 w-8 items-center justify-center overflow-hidden rounded-full bg-[#0F172A] p-0.5">
              <Image
                src={SITE_CONFIG.media.logo}
                alt={SITE_CONFIG.name}
                width={56}
                height={56}
                className="h-full w-full object-contain"
              />
            </span>
            <span className="font-display text-sm font-semibold tracking-[-0.02em] text-[#0F172A] sm:text-base">
              {SITE_CONFIG.name}
            </span>
          </Link>

          <Link
            href={SITE_CONFIG.routes.home}
            className="inline-flex items-center gap-1.5 text-xs font-semibold text-[#475569] transition-colors hover:text-[#0F172A] sm:text-sm"
          >
            <svg
              aria-hidden="true"
              viewBox="0 0 16 16"
              fill="none"
              className="h-3.5 w-3.5 stroke-current stroke-[1.8]"
            >
              <path
                d="M12.5 8H3.5M3.5 8L7.25 4.25M3.5 8L7.25 11.75"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <span>Inicio</span>
          </Link>
        </div>
      </header>

      {/* Tarjeta central puente */}
      <main className="mx-auto flex w-full max-w-xl flex-1 flex-col justify-center px-5 py-12 sm:px-8">
        <section className="rounded-3xl border border-[#E2E8F0] bg-[#FFFFFF] p-7 shadow-[0_24px_56px_-20px_rgba(15,23,42,0.08)] sm:p-10">
          <div className="inline-flex items-center gap-2 rounded-full border border-[#E2E8F0] bg-[#F8FAFC] px-3 py-1">
            <span className="h-2 w-2 rounded-full bg-[#10B981]" />
            <span className="tabular-mono text-[11px] font-medium tracking-[0.14em] text-[#475569] uppercase">
              {badge}
            </span>
          </div>

          <h1 className="font-display mt-5 text-[clamp(1.75rem,4vw,2.45rem)] leading-[1.02] font-bold tracking-[-0.035em] text-[#0F172A]">
            {title}
          </h1>

          <p className="mt-3.5 text-[15px] leading-relaxed text-[#475569] sm:text-base">
            {description}
          </p>

          <ul className="mt-6 divide-y divide-[#E2E8F0] rounded-2xl border border-[#E2E8F0] bg-[#F8FAFC] px-4">
            {features.map((item) => (
              <li
                key={item.code}
                className="flex items-center gap-3 py-3 text-sm text-[#0F172A]"
              >
                <span className="tabular-mono text-xs font-medium text-[#2563EB]">
                  {item.code}
                </span>
                <span className="font-medium text-[#0F172A]">{item.label}</span>
              </li>
            ))}
          </ul>

          <div className="mt-7 flex flex-col gap-3 sm:flex-row sm:items-center">
            <Link
              href={SITE_CONFIG.routes.home}
              className="inline-flex min-h-[48px] flex-1 items-center justify-center gap-2 rounded-full bg-[#2563EB] px-6 py-3 text-sm font-semibold text-white transition-colors duration-200 hover:bg-[#1D4ED8] focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[#2563EB]"
            >
              <svg
                aria-hidden="true"
                viewBox="0 0 16 16"
                fill="none"
                className="h-4 w-4 stroke-current stroke-[1.9]"
              >
                <path
                  d="M12.5 8H3.5M3.5 8L7.25 4.25M3.5 8L7.25 11.75"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
              <span>Volver a la portada</span>
            </Link>

            {secondaryAction ? (
              secondaryAction.external ? (
                <a
                  href={secondaryAction.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex min-h-[48px] items-center justify-center rounded-full border border-[#E2E8F0] bg-[#FFFFFF] px-5 py-3 text-sm font-semibold text-[#0F172A] transition-colors hover:border-[#0F172A]/25 hover:bg-[#F8FAFC]"
                >
                  {secondaryAction.label}
                </a>
              ) : (
                <Link
                  href={secondaryAction.href}
                  className="inline-flex min-h-[48px] items-center justify-center rounded-full border border-[#E2E8F0] bg-[#FFFFFF] px-5 py-3 text-sm font-semibold text-[#0F172A] transition-colors hover:border-[#0F172A]/25 hover:bg-[#F8FAFC]"
                >
                  {secondaryAction.label}
                </Link>
              )
            ) : null}
          </div>
        </section>
      </main>

      <footer className="border-t border-[#E2E8F0] py-4 text-center">
        <p className="tabular-mono text-[11px] tracking-[0.14em] text-[#64748B] uppercase">
          ESPACIO OPERATIVO (APP) · ARQUITECTURA DESACOPLADA GO + NEXT.JS
        </p>
      </footer>
    </div>
  );
}
