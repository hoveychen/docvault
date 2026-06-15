import {
  File,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Presentation,
  type LucideIcon,
} from "lucide-react";
import type { DocItem } from "../api";

export interface FileVisual {
  Icon: LucideIcon;
  color: string; // hue for the icon tint
  label: string; // short type label
}

const TEXT = "#5b8def";
const SHEET = "#3fb27f";
const SLIDE = "#e0823d";
const IMAGE = "#c06ad9";
const CODE = "#56b6c2";
const AUDIO = "#d97aa6";
const VIDEO = "#e06c75";
const ARCHIVE = "#c9a227";
const GENERIC = "#8a8f98";

const BY_FORMAT: Record<string, FileVisual> = {
  docx: { Icon: FileText, color: TEXT, label: "DOCX" },
  doc: { Icon: FileText, color: TEXT, label: "DOC" },
  pdf: { Icon: FileText, color: VIDEO, label: "PDF" },
  txt: { Icon: FileText, color: GENERIC, label: "TXT" },
  md: { Icon: FileText, color: TEXT, label: "MD" },
  xlsx: { Icon: FileSpreadsheet, color: SHEET, label: "XLSX" },
  xls: { Icon: FileSpreadsheet, color: SHEET, label: "XLS" },
  csv: { Icon: FileSpreadsheet, color: SHEET, label: "CSV" },
  pptx: { Icon: Presentation, color: SLIDE, label: "PPTX" },
  ppt: { Icon: Presentation, color: SLIDE, label: "PPT" },
  png: { Icon: FileImage, color: IMAGE, label: "PNG" },
  jpg: { Icon: FileImage, color: IMAGE, label: "JPG" },
  jpeg: { Icon: FileImage, color: IMAGE, label: "JPEG" },
  gif: { Icon: FileImage, color: IMAGE, label: "GIF" },
  svg: { Icon: FileImage, color: IMAGE, label: "SVG" },
  webp: { Icon: FileImage, color: IMAGE, label: "WEBP" },
  zip: { Icon: FileArchive, color: ARCHIVE, label: "ZIP" },
  gz: { Icon: FileArchive, color: ARCHIVE, label: "GZ" },
  tar: { Icon: FileArchive, color: ARCHIVE, label: "TAR" },
  mp3: { Icon: FileAudio, color: AUDIO, label: "MP3" },
  wav: { Icon: FileAudio, color: AUDIO, label: "WAV" },
  mp4: { Icon: FileVideo, color: VIDEO, label: "MP4" },
  mov: { Icon: FileVideo, color: VIDEO, label: "MOV" },
  json: { Icon: FileCode, color: CODE, label: "JSON" },
  js: { Icon: FileCode, color: CODE, label: "JS" },
  ts: { Icon: FileCode, color: CODE, label: "TS" },
};

// Feishu/Lark native doc_type fallbacks when no exported format is present.
const BY_DOCTYPE: Record<string, FileVisual> = {
  docx: { Icon: FileText, color: TEXT, label: "文档" },
  doc: { Icon: FileText, color: TEXT, label: "文档" },
  sheet: { Icon: FileSpreadsheet, color: SHEET, label: "表格" },
  bitable: { Icon: FileSpreadsheet, color: SHEET, label: "多维表格" },
  slides: { Icon: Presentation, color: SLIDE, label: "幻灯片" },
  mindnote: { Icon: FileCode, color: CODE, label: "思维笔记" },
  file: { Icon: File, color: GENERIC, label: "文件" },
};

export function fileVisual(doc: Pick<DocItem, "format" | "doc_type">): FileVisual {
  const fmt = (doc.format || "").toLowerCase();
  if (fmt && BY_FORMAT[fmt]) return BY_FORMAT[fmt];
  const dt = (doc.doc_type || "").toLowerCase();
  if (dt && BY_DOCTYPE[dt]) return BY_DOCTYPE[dt];
  if (fmt) return { Icon: File, color: GENERIC, label: fmt.toUpperCase() };
  return { Icon: File, color: GENERIC, label: dt || "文件" };
}
