import React, { useState, useEffect, useRef } from "react";
// @ts-ignore
import { DownloadSite } from "../../wailsjs/go/main/App";
import { useTranslation } from "../i18n";
import { useApp } from "../context/AppContext";

const DownloadView = () => {
  const { t } = useTranslation();
  const { isDownloading, setIsDownloading, downloadLogs, setDownloadLogs } = useApp();
  const [url, setUrl] = useState("");
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: "smooth" });
    }
  }, [downloadLogs]);

  const handleDownload = async () => {
    if (!url) return;
    setDownloadLogs([`> Starting download: ${url}`]);
    setIsDownloading(true);
    try {
      const res = await DownloadSite(url, "downloads");
      // Check for backend errors returned as strings or early exits
      if (res && res.startsWith("Error")) {
        setDownloadLogs(prev => [...prev, `[System] ${res}`]);
        setIsDownloading(false);
      } else if (res === "Download already in progress") {
        setDownloadLogs(prev => [...prev, "[System] This URL is already being downloaded."]);
        setIsDownloading(false);
      }
    } catch (err) {
      setDownloadLogs((prev) => [...prev, `[Bridge Error] ${err}`]);
      setIsDownloading(false);
    }
  };

  const handleForceReset = () => {
    setIsDownloading(false);
    setDownloadLogs(prev => [...prev, "[System] Manual UI state reset by user."]);
  };

  return (
    <div className="flex flex-col h-full gap-6">
      <div className="bg-graphite-800/40 backdrop-blur-md rounded-2xl p-6 border border-white/5 shadow-xl transition-all hover:bg-graphite-800/60">
        <h2 className="text-2xl font-bold mb-4 bg-clip-text text-transparent bg-gradient-to-r from-neon-cyan to-white">
          {t("new_download")}
        </h2>
        <div className="flex gap-4">
          <input
            type="text"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder={t("url_placeholder")}
            onKeyDown={(e) => e.key === "Enter" && handleDownload()}
            className="flex-1 bg-black/40 border border-white/10 rounded-xl px-4 py-3 text-white placeholder-gray-600 focus:outline-none focus:border-neon-cyan/50 focus:ring-1 focus:ring-neon-cyan/50 transition-all font-mono"
          />
          <div className="flex gap-2">
            <button
              onClick={handleDownload}
              disabled={isDownloading}
              className={`px-8 py-3 rounded-xl font-bold transition-all shadow-lg flex items-center gap-2 ${isDownloading
                ? "bg-gray-700/50 text-gray-400 cursor-not-allowed border border-white/5"
                : "bg-gradient-to-r from-neon-cyan to-blue-600 hover:shadow-neon-cyan/25 hover:scale-105 active:scale-95 text-white"
                }`}
            >
              {isDownloading ? (
                <>
                  <span className="animate-spin text-xl">⚙️</span>{" "}
                  {t("processing")}
                </>
              ) : (
                <>🚀 {t("start")}</>
              )}
            </button>
            {isDownloading && (
              <button
                onClick={handleForceReset}
                className="w-12 h-12 flex items-center justify-center bg-red-500/10 hover:bg-red-500/20 text-red-500 rounded-xl border border-red-500/10 transition-all group/reset"
                title="Force Reset UI State"
              >
                <span className="group-hover:rotate-90 transition-transform font-bold">✕</span>
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="flex-1 bg-black/90 rounded-2xl border border-white/10 p-4 font-mono text-sm overflow-hidden flex flex-col shadow-2xl relative group">
        <div className="absolute top-0 left-0 right-0 h-10 bg-white/5 flex items-center px-4 gap-2 border-b border-white/5 select-none z-10 transition-colors group-hover:bg-white/10">
          <div className="flex gap-2">
            <div className="w-3 h-3 rounded-full bg-red-500/60 group-hover:bg-red-500 transition-colors"></div>
            <div className="w-3 h-3 rounded-full bg-yellow-500/60 group-hover:bg-yellow-500 transition-colors"></div>
            <div className="w-3 h-3 rounded-full bg-green-500/60 group-hover:bg-green-500 transition-colors"></div>
          </div>
          <span className="ml-4 text-xs text-gray-500 font-sans tracking-wide">
            {t("terminal")} — {t("worker_pool")}
          </span>
        </div>

        <div className="mt-10 flex-1 overflow-y-auto space-y-1.5 p-2 font-mono scrollbar-custom">
          {downloadLogs.length === 0 && (
            <div className="h-full flex items-center justify-center text-gray-700">
              <span className="italic">{t("waiting")}</span>
            </div>
          )}
          {downloadLogs.map((log, i) => (
            <div
              key={i}
              className="text-gray-300 break-all flex items-start hover:bg-white/5 rounded px-2 py-0.5 transition-colors"
            >
              <span className="text-neon-cyan/60 mr-3 select-none">➜</span>
              <span
                className={
                  log.includes("Error")
                    ? "text-red-400"
                    : log.includes("Done") ||
                      log.includes("Success") ||
                      log.includes("DONE") ||
                      log.includes("complete")
                      ? "text-green-400"
                      : "text-gray-300"
                }
              >
                {log}
              </span>
            </div>
          ))}
          <div ref={logEndRef} />
        </div>
      </div>
    </div>
  );
};

export default DownloadView;
