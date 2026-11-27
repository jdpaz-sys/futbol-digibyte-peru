import "@/landing/styles/landing.css";

export default function LandingLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <div className="landing-shell-viewport bg-[#070B14] text-white">
      {children}
    </div>
  );
}
