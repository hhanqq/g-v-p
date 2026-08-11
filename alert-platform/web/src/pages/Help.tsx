import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Card, PageHeader } from "../components/ui";
import { HELP_SECTIONS, findHelpArticle } from "../content/help";

export default function Help() {
  const [params, setParams] = useSearchParams();
  const requested = params.get("a");
  const [activeId, setActiveId] = useState(requested ?? HELP_SECTIONS[0].articles[0].id);

  useEffect(() => {
    if (requested && requested !== activeId) {
      setActiveId(requested);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requested]);

  const active = findHelpArticle(activeId) ?? HELP_SECTIONS[0].articles[0];

  function select(id: string) {
    setActiveId(id);
    setParams(id === HELP_SECTIONS[0].articles[0].id ? {} : { a: id });
  }

  return (
    <div>
      <PageHeader title="Справка" />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[260px_1fr]">
        <Card className="!p-3">
          <nav className="space-y-4">
            {HELP_SECTIONS.map((section) => (
              <div key={section.title}>
                <div className="px-2 text-xs font-semibold uppercase tracking-wide text-muted">{section.title}</div>
                <div className="mt-1">
                  {section.articles.map((article) => (
                    <button
                      key={article.id}
                      onClick={() => select(article.id)}
                      className={`block w-full rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                        active.id === article.id ? "bg-accent/15 text-accent" : "text-fg hover:bg-fg/5"
                      }`}
                    >
                      {article.title}
                    </button>
                  ))}
                </div>
              </div>
            ))}
          </nav>
        </Card>
        <Card>
          <h2 className="text-lg font-semibold">{active.title}</h2>
          <div className="mt-3 space-y-3 text-sm leading-relaxed text-fg">
            {active.paragraphs.map((p, i) => (
              <p key={i}>{p}</p>
            ))}
          </div>
        </Card>
      </div>
    </div>
  );
}
