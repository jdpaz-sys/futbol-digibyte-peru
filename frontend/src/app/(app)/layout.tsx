export default function WebAppLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return <div className="min-h-dvh bg-[#F8FAFC] text-[#0F172A]">{children}</div>;
}
