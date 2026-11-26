import "@/landing/styles/landing.css";

export default function LandingLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return <div className="min-h-dvh bg-[#070B14] text-white">{children}</div>;
}
