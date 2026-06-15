import { useParams } from "react-router-dom";
import { Browser } from "../components/Browser";
import { pathFromSplat } from "../lib/routes";

// Thin route adapter — resolves the mode + path, then renders the Browser.
export function Browse({ mode }: { mode: "folder" | "recent" | "source" }) {
  const params = useParams();
  if (mode === "source") {
    return <Browser mode="source" provider={params.provider || ""} />;
  }
  if (mode === "recent") {
    return <Browser mode="recent" />;
  }
  return <Browser mode="folder" path={pathFromSplat(params["*"])} />;
}
