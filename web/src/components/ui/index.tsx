import type {
  ButtonHTMLAttributes,
  InputHTMLAttributes,
  ReactNode,
} from "react";
import type { LucideIcon } from "lucide-react";
import "./ui.css";

function cx(...parts: (string | false | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

/* ---------- Button ---------- */
type ButtonVariant = "default" | "primary" | "ghost" | "danger";
interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: "sm" | "md" | "lg";
  block?: boolean;
  icon?: LucideIcon;
}
export function Button({
  variant = "default",
  size = "md",
  block,
  icon: Icon,
  className,
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      className={cx(
        "btn",
        variant !== "default" && `btn--${variant}`,
        size !== "md" && `btn--${size}`,
        block && "btn--block",
        className,
      )}
      {...rest}
    >
      {Icon && <Icon />}
      {children}
    </button>
  );
}

/* ---------- IconButton ---------- */
interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon: LucideIcon;
  active?: boolean;
  size?: "sm" | "md";
  label?: string;
}
export function IconButton({
  icon: Icon,
  active,
  size = "md",
  label,
  className,
  ...rest
}: IconButtonProps) {
  const btn = (
    <button
      className={cx("icon-btn", size === "sm" && "icon-btn--sm", active && "icon-btn--active", className)}
      aria-label={label}
      {...rest}
    >
      <Icon />
    </button>
  );
  return label ? <Tooltip label={label}>{btn}</Tooltip> : btn;
}

/* ---------- Input ---------- */
interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  icon?: LucideIcon;
  block?: boolean;
}
export function Input({ icon: Icon, block, className, ...rest }: InputProps) {
  return (
    <div className={cx("input-wrap", block && "input-wrap--block", className)}>
      {Icon && <Icon />}
      <input {...rest} />
    </div>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
    </label>
  );
}

/* ---------- Badge ---------- */
type Tone = "neutral" | "accent" | "danger" | "success" | "warning";
export function Badge({
  tone = "neutral",
  dot,
  children,
}: {
  tone?: Tone;
  dot?: boolean;
  children: ReactNode;
}) {
  return <span className={cx("badge", `badge--${tone}`, dot && "badge--dot")}>{children}</span>;
}

/* ---------- Avatar ---------- */
export function Avatar({
  src,
  name,
  size = 26,
}: {
  src?: string;
  name?: string;
  size?: number;
}) {
  const initial = (name || "?").trim().charAt(0).toUpperCase();
  return (
    <span
      className="avatar"
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}
    >
      {src ? <img src={src} alt="" /> : initial}
    </span>
  );
}

/* ---------- Spinner ---------- */
export function Spinner({ size = 16 }: { size?: number }) {
  return <span className="spinner" style={{ width: size, height: size }} aria-label="加载中" />;
}

/* ---------- Skeleton ---------- */
export function Skeleton({
  width,
  height = 14,
  radius,
}: {
  width?: number | string;
  height?: number | string;
  radius?: number | string;
}) {
  return <span className="skeleton" style={{ display: "block", width, height, borderRadius: radius }} />;
}

/* ---------- Tooltip ---------- */
export function Tooltip({ label, children }: { label: string; children: ReactNode }) {
  return (
    <span className="tip">
      {children}
      <span className="tip__bubble" role="tooltip">
        {label}
      </span>
    </span>
  );
}
