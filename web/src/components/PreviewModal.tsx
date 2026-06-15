import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ArrowDownToLine, X } from "lucide-react";
import { api, type DocItem } from "../api";
import { fileVisual } from "../lib/fileType";
import { previewKind, fetchDoc, type PreviewKind } from "../lib/preview";
import { formatSize } from "../lib/format";
import { Badge, IconButton, Spinner } from "./ui";

interface Props {
  doc: DocItem;
  onClose: () => void;
}

type Status = "loading" | "ready" | "error";
type Sheet = { name: string; html: string };

export function PreviewModal({ doc, onClose }: Props) {
  const kind = previewKind(doc);
  const v = fileVisual(doc);

  const [status, setStatus] = useState<Status>(
    kind === "unsupported" ? "ready" : "loading",
  );
  const [errMsg, setErrMsg] = useState("");
  const [url, setUrl] = useState("");        // pdf / image object URL
  const [text, setText] = useState("");      // text content
  const [sheets, setSheets] = useState<Sheet[]>([]);
  const [activeSheet, setActiveSheet] = useState(0);
  const docxBuf = useRef<ArrayBuffer | null>(null);
  const docxHost = useRef<HTMLDivElement | null>(null);

  // Esc to close + lock background scroll while open.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  // Load the bytes for the relevant preview kind.
  useEffect(() => {
    if (kind === "unsupported") return;
    let revoke = "";
    let cancelled = false;

    (async () => {
      try {
        if (kind === "pdf" || kind === "image") {
          const blob = await fetchDoc(doc.id, "blob");
          if (cancelled) return;
          revoke = URL.createObjectURL(blob);
          setUrl(revoke);
        } else if (kind === "text") {
          const t = await fetchDoc(doc.id, "text");
          if (cancelled) return;
          setText(t);
        } else if (kind === "sheet") {
          const buf = await fetchDoc(doc.id, "arraybuffer");
          if (cancelled) return;
          const XLSX = await import("xlsx");
          const wb = XLSX.read(buf, { type: "array" });
          setSheets(
            wb.SheetNames.map((name) => ({
              name,
              html: XLSX.utils.sheet_to_html(wb.Sheets[name]),
            })),
          );
        } else if (kind === "docx") {
          const buf = await fetchDoc(doc.id, "arraybuffer");
          if (cancelled) return;
          docxBuf.current = buf;
        }
        if (!cancelled) setStatus("ready");
      } catch (e) {
        if (!cancelled) {
          setErrMsg(e instanceof Error ? e.message : "加载失败");
          setStatus("error");
        }
      }
    })();

    return () => {
      cancelled = true;
      if (revoke) URL.revokeObjectURL(revoke);
    };
  }, [doc.id, kind]);

  // Render docx into the host once the buffer is ready and the node is mounted.
  useEffect(() => {
    if (kind !== "docx" || status !== "ready") return;
    const host = docxHost.current;
    const buf = docxBuf.current;
    if (!host || !buf) return;
    let cancelled = false;
    (async () => {
      try {
        const { renderAsync } = await import("docx-preview");
        if (cancelled) return;
        host.innerHTML = "";
        await renderAsync(buf, host, undefined, { inWrapper: true });
      } catch (e) {
        if (!cancelled) {
          setErrMsg(e instanceof Error ? e.message : "渲染失败");
          setStatus("error");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [kind, status]);

  return createPortal(
    <div className="preview-overlay" onClick={onClose}>
      <div
        className="preview-modal"
        role="dialog"
        aria-modal="true"
        aria-label={doc.title}
        onClick={(e) => e.stopPropagation()}
      >
        <header className="preview-modal__head">
          <v.Icon color={v.color} />
          <span className="preview-modal__title" title={doc.title}>
            {doc.title}
          </span>
          <Badge tone="neutral">{v.label}</Badge>
          <span className="preview-modal__spacer" />
          <a
            href={api.downloadUrl(doc.id)}
            className="icon-btn icon-btn--sm"
            aria-label="下载"
            title="下载"
          >
            <ArrowDownToLine />
          </a>
          <IconButton icon={X} size="sm" onClick={onClose} label="关闭" />
        </header>

        <div className="preview-modal__body">
          <Body
            kind={kind}
            status={status}
            errMsg={errMsg}
            url={url}
            text={text}
            sheets={sheets}
            activeSheet={activeSheet}
            onSheet={setActiveSheet}
            title={doc.title}
            docxHost={docxHost}
            downloadUrl={api.downloadUrl(doc.id)}
          />
        </div>

        {doc.attachments && doc.attachments.length > 0 && (
          <footer className="preview-modal__attachments">
            <span className="preview-modal__attachments-label">
              内嵌附件 · {doc.attachments.length}
            </span>
            <ul className="preview-modal__attachments-list">
              {doc.attachments.map((a) => (
                <li key={a.id}>
                  <a
                    href={api.attachmentDownloadUrl(doc.id, a.id)}
                    className="preview-modal__attachment"
                    title={`下载 ${a.filename}`}
                  >
                    <ArrowDownToLine size={14} />
                    <span className="preview-modal__attachment-name">
                      {a.filename || "附件"}
                    </span>
                    <span className="preview-modal__attachment-size">
                      {formatSize(a.size_bytes)}
                    </span>
                  </a>
                </li>
              ))}
            </ul>
          </footer>
        )}
      </div>
    </div>,
    document.body,
  );
}

function Body(p: {
  kind: PreviewKind;
  status: Status;
  errMsg: string;
  url: string;
  text: string;
  sheets: Sheet[];
  activeSheet: number;
  onSheet: (i: number) => void;
  title: string;
  docxHost: React.RefObject<HTMLDivElement>;
  downloadUrl: string;
}) {
  if (p.kind === "unsupported") {
    return <Fallback msg="该格式暂不支持在线预览" downloadUrl={p.downloadUrl} />;
  }
  if (p.status === "error") {
    return <Fallback msg={p.errMsg || "加载失败"} downloadUrl={p.downloadUrl} />;
  }
  if (p.status === "loading" && p.kind !== "docx") {
    return (
      <div className="preview-center">
        <Spinner size={22} />
      </div>
    );
  }

  switch (p.kind) {
    case "pdf":
      return <iframe className="preview-frame" src={p.url} title={p.title} />;
    case "image":
      return (
        <div className="preview-center">
          <img className="preview-img" src={p.url} alt={p.title} />
        </div>
      );
    case "text":
      return <pre className="preview-text">{p.text}</pre>;
    case "sheet":
      return (
        <div className="preview-sheet">
          {p.sheets.length > 1 && (
            <div className="preview-sheet__tabs">
              {p.sheets.map((s, i) => (
                <button
                  key={s.name}
                  className={"preview-sheet__tab" + (i === p.activeSheet ? " is-active" : "")}
                  onClick={() => p.onSheet(i)}
                >
                  {s.name}
                </button>
              ))}
            </div>
          )}
          <div
            className="preview-sheet__grid"
            dangerouslySetInnerHTML={{ __html: p.sheets[p.activeSheet]?.html || "" }}
          />
        </div>
      );
    case "docx":
      return (
        <>
          {p.status === "loading" && (
            <div className="preview-center">
              <Spinner size={22} />
            </div>
          )}
          <div ref={p.docxHost} className="preview-docx" />
        </>
      );
    default:
      return <Fallback msg="该格式暂不支持在线预览" downloadUrl={p.downloadUrl} />;
  }
}

function Fallback({ msg, downloadUrl }: { msg: string; downloadUrl: string }) {
  return (
    <div className="preview-center preview-fallback">
      <p>{msg}</p>
      <a href={downloadUrl} className="btn btn--primary btn--sm">
        <ArrowDownToLine />
        下载文件
      </a>
    </div>
  );
}
