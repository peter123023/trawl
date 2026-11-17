import { useEffect, useState } from "react";
import Rail from "./components/Rail";
import Desk from "./pages/Desk";
import Case from "./pages/Case";
import Queue from "./pages/Queue";
import Prompts from "./pages/Prompts";
import Datasets from "./pages/Datasets";
import Settings from "./pages/Settings";

export type Route =
  | { kind: "desk" }
  | { kind: "case"; traceId: string }
  | { kind: "queue" }
  | { kind: "queueCase"; traceId: string }
  | { kind: "prompts" }
  | { kind: "datasets"; id?: string }
  | { kind: "settings" };

export function parseRoute(path: string): Route {
  const seg = path.split("/").filter(Boolean);
  if (seg[0] === "queue" && seg[1]) return { kind: "queueCase", traceId: decodeURIComponent(seg[1]) };
  if (seg[0] === "queue") return { kind: "queue" };
  if (seg[0] === "prompts") return { kind: "prompts" };
  if (seg[0] === "datasets") return { kind: "datasets" };
  if (seg[0] === "settings") return { kind: "settings" };
  if (seg[0] === "t" && seg[1]) return { kind: "case", traceId: decodeURIComponent(seg[1]) };
  return { kind: "desk" };
}

export function navigate(to: string) {
  if (window.location.pathname === to) return;
  window.history.pushState({}, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export default function App() {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname));

  useEffect(() => {
    const onPop = () => setRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const rail =
    route.kind === "queue" || route.kind === "queueCase"
      ? "queue"
      : route.kind === "case"
        ? "case"
        : route.kind === "prompts"
          ? "prompts"
          : route.kind === "datasets"
            ? "datasets"
            : route.kind === "settings"
              ? "settings"
              : "desk";

  return (
    <>
      <Rail active={rail} />
      {route.kind === "desk" && <Desk />}
      {route.kind === "case" && <Case traceId={route.traceId} />}
      {route.kind === "queue" && <Queue />}
      {route.kind === "prompts" && <Prompts />}
      {route.kind === "datasets" && <Datasets />}
      {route.kind === "settings" && <Settings />}
      {route.kind === "queueCase" && <Case traceId={route.traceId} queueMode />}
    </>
  );
}
