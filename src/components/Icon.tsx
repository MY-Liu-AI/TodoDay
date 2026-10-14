export default function Icon({
  name,
  size = 18,
}: {
  name: string;
  size?: number;
}) {
  const paths: Record<string, string> = {
    plus: "M12 5v14M5 12h14",
    check: "m5 12 4 4L19 6",
    list: "M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01",
    calendar:
      "M7 2v4M17 2v4M3 10h18M5 4h14a2 2 0 0 1 2 2v14H3V6a2 2 0 0 1 2-2Z",
    stats: "M4 20V12M10 20V4M16 20v-9M22 20H2",
    arrow: "m9 5 7 7-7 7",
    search: "m21 21-5-5M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0",
    sun: "M12 3v1M12 20v1M3 12h1M20 12h1M5 5l1 1M18 18l1 1M5 19l1-1M18 6l1-1M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
    moon: "M20 15A9 9 0 0 1 9 3a9 9 0 1 0 11 12Z",
    close: "m6 6 12 12M6 18 18 6",
    download: "M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5",
    help: "M9 8a3 3 0 1 1 4 3c-1 1-1 2-1 3M12 17h.01M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0",
  };
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name] || paths.list} />
    </svg>
  );
}
