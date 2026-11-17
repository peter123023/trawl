import { navigate } from "../App";

type Active = "desk" | "case" | "queue" | "prompts" | "datasets" | "settings";

export default function Rail({ active }: { active: Active }) {
  const items: { key: Active; glyph: string; label: string; to: string }[] = [
    { key: "desk", glyph: "☰", label: "案件台账", to: "/" },
    { key: "queue", glyph: "✓", label: "待判队列", to: "/queue" },
    { key: "prompts", glyph: "⌗", label: "Prompt 版本", to: "/prompts" },
    { key: "datasets", glyph: "▤", label: "回归集", to: "/datasets" },
  ];
  return (
    <nav className="rail">
      <div className="mark">T</div>
      {items.map((it) => (
        <a
          key={it.key}
          className={`item ${active === it.key ? "active" : ""}`}
          href={it.to}
          title={it.label}
          onClick={(e) => {
            e.preventDefault();
            navigate(it.to);
          }}
        >
          {it.glyph}
        </a>
      ))}
      <a
        className={`item ${active === "settings" ? "active" : ""}`}
        href="/settings"
        title="设置与备份"
        onClick={(e) => {
          e.preventDefault();
          navigate("/settings");
        }}
      >
        ⚙
      </a>
    </nav>
  );
}
