"use client";

import Image from "next/image";
import Link from "next/link";
import { SITE_CONFIG } from "@/shared/site-config";
import { useScrollThreshold } from "@/landing/hooks/useScrollThreshold";

export function LandingHeader() {
  const { isScrolled, isOnLightSurface } = useScrollThreshold(24);

  return (
    <header
      className={`fixed inset-x-0 top-0 z-40 px-3.5 transition-all duration-500 ease-[cubic-bezier(0.16,1,0.3,1)] sm:px-6 lg:px-10 ${
        isScrolled ? "pt-2.5 sm:pt-3.5" : "pt-3.5 sm:pt-5"
      }`}
    >
      <div
        className={`mx-auto flex max-w-7xl items-center justify-between gap-2 transition-all duration-500 ease-[cubic-bezier(0.22,1,0.36,1)] ${
          isOnLightSurface
            ? "header-glass-light rounded-full border border-[#0F172A]/10 px-3.5 py-2 sm:px-5 sm:py-2.5"
            : isScrolled
              ? "header-glass-dark rounded-full border border-white/15 bg-[#0F172A]/35 px-3.5 py-2 backdrop-blur-xl sm:px-5 sm:py-2.5"
              : "rounded-full border border-transparent bg-transparent px-1.5 py-1.5 sm:px-2"
        }`}
      >
        {/* Marca izquierda limpia */}
        <Link
          href={SITE_CONFIG.routes.home}
          aria-label={`Ir al inicio de ${SITE_CONFIG.name}`}
          className="group flex min-w-0 items-center gap-2 rounded-full focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white/70 sm:gap-2.5"
        >
          <Image
            src={SITE_CONFIG.media.logo}
            alt={SITE_CONFIG.name}
            width={34}
            height={34}
            priority
            className="h-8 w-8 shrink-0 object-contain transition-transform duration-300 group-hover:scale-105 sm:h-[34px] sm:w-[34px]"
          />
          <span
            className={`font-display truncate text-[13px] font-semibold tracking-[-0.02em] transition-colors duration-300 sm:text-[15px] md:text-base ${
              isOnLightSurface ? "text-[#070B14]" : "text-white"
            }`}
          >
            {SITE_CONFIG.name}
          </span>
        </Link>

        {/* Navegación derecha minimalista */}
        <nav
          aria-label="Accesos principales"
          className="flex shrink-0 items-center gap-3 sm:gap-5"
        >
          <Link
            href={SITE_CONFIG.routes.login}
            className={`relative py-1.5 text-[12.5px] font-medium whitespace-nowrap transition-colors duration-200 focus-visible:outline-2 focus-visible:outline-offset-4 sm:text-sm ${
              isOnLightSurface
                ? "text-[#070B14]/70 hover:text-[#070B14] focus-visible:outline-[#070B14]"
                : "text-white/80 hover:text-white focus-visible:outline-white/70"
            }`}
          >
            Soy dueño
          </Link>

          <Link
            href={SITE_CONFIG.routes.explore}
            className={`inline-flex min-h-[36px] items-center gap-1.5 rounded-full border px-3.5 py-1.5 text-[12.5px] font-semibold whitespace-nowrap transition-colors duration-300 focus-visible:outline-2 focus-visible:outline-offset-4 sm:min-h-[40px] sm:px-4.5 sm:py-2 sm:text-sm ${
              isOnLightSurface
                ? "border-[#070B14]/15 bg-[#070B14] text-white hover:border-[#2563EB] hover:bg-[#2563EB] focus-visible:outline-[#070B14]"
                : "header-cta-glass-dark border-white/15 bg-white/[0.06] text-white hover:border-white hover:bg-white hover:text-[#070B14] focus-visible:outline-white/70"
            }`}
          >
            <span>Abrir App</span>
            <svg
              aria-hidden="true"
              viewBox="0 0 16 16"
              fill="none"
              className="h-3.5 w-3.5 stroke-current stroke-[1.75]"
            >
              <path
                d="M4.5 11.5L11.5 4.5M11.5 4.5H5.75M11.5 4.5V10.25"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </Link>
        </nav>
      </div>
    </header>
  );
}
