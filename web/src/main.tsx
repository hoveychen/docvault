import React from "react";
import ReactDOM from "react-dom/client";
import { App } from "./App";
import { ThemeProvider } from "./lib/theme";
import "./lib/i18n"; // initializes i18next before any component renders
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/app.css";
import "./styles/pages.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ThemeProvider>
      <App />
    </ThemeProvider>
  </React.StrictMode>,
);
