import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import zh from "../locales/zh";
import en from "../locales/en";
import ja from "../locales/ja";

export const SUPPORTED_LANGS = ["zh", "en", "ja"] as const;
export type Lang = (typeof SUPPORTED_LANGS)[number];

const STORAGE_KEY = "docvault-lang";

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      zh: { translation: zh },
      en: { translation: en },
      ja: { translation: ja },
    },
    supportedLngs: SUPPORTED_LANGS as unknown as string[],
    // Treat region variants (zh-CN, ja-JP, en-US) as their base language.
    load: "languageOnly",
    nonExplicitSupportedLngs: true,
    fallbackLng: "zh",
    detection: {
      // Honor the user's saved choice first, then the browser's language.
      order: ["localStorage", "navigator", "htmlTag"],
      lookupLocalStorage: STORAGE_KEY,
      caches: ["localStorage"],
    },
    interpolation: {
      escapeValue: false, // React already escapes against XSS.
    },
  });

export default i18n;
