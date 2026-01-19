import React, {
  useState,
  useEffect,
  useRef,
  useCallback,
  useMemo,
} from "react";
// @ts-ignore
import { DownloadSite } from "../../wailsjs/go/main/App";
// @ts-ignore
import { EventsOn } from "../../wailsjs/runtime";
import { useTranslation } from "../i18n";
import { useApp } from "../context/AppContext";

const LogEntry = React.memo(({ log }: { log: string }) => {
  const isError = log.includes("Error") || log.includes("failed");
  const isSuccess = useMemo(
    () => /Done|Success|DONE|complete|завершено/i.test(log),
    [log],
  );

  return (
    <div className="text-gray-300 break-all flex items-start hover:bg-white/5 rounded px-2 py-0.5 transition-colors group/line">
      <span className="text-neon-cyan/60 mr-3 select-none opacity-40 group-hover/line:opacity-100 transition-opacity">
        ➜
      </span>
      <span
        className={
          isError
            ? "text-red-400"
            : isSuccess
              ? "text-green-400"
              : "text-gray-300"
        }
      >
        {log}
      </span>
    </div>
  );
});

const DownloadView = () => {
  const { t } = useTranslation();
  const { isDownloading, setIsDownloading, downloadLogs, setDownloadLogs } =
    useApp();
  const [url, setUrl] = useState("");
  const [progress, setProgress] = useState({ current: 0, total: 0 });
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (logEndRef.current)
      logEndRef.current.scrollIntoView({ behavior: "smooth" });
  }, [downloadLogs.length]);

  useEffect(() => {
    const minTime = setTimeout(() => {}, 0);
    const clProgress = EventsOn("download:progress", (data: any) => {
      setProgress({ current: data.current, total: data.total });
    });
    const clDone = EventsOn("download:done", () => {
      setIsDownloading(false);
      setProgress({ current: 0, total: 0 });
    });

    // Handle tab switching
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        // Refresh progress when tab becomes visible
        EventsOn("download:progress", (data: any) => {
          setProgress({ current: data.current, total: data.total });
        });
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      clProgress();
      clDone();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [setIsDownloading]);

  const downloadPercent = useMemo(
    () =>
      progress.total > 0
        ? Math.min(Math.round((progress.current / progress.total) * 100), 100)
        : 0,
    [progress],
  );

  const handleDownload = useCallback(async () => {
    if (!url) return;
    setDownloadLogs([`> Инициализация захвата: ${url}`]);
    setIsDownloading(true);
    setProgress({ current: 0, total: 0 });
    try {
      const res = await DownloadSite(url, "downloads");
      if (res && res.startsWith("Error")) {
        setDownloadLogs((prev) => [...prev, `[System] ${res}`]);
        setIsDownloading(false);
      }
    } catch (err) {
      setDownloadLogs((prev) => [...prev, `[Bridge Error] ${err}`]);
      setIsDownloading(false);
    }
  }, [url, setDownloadLogs, setIsDownloading]);

  return (
    <div className="flex flex-col h-full gap-6 animate-fade-in">
      {/* Input Section */}
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
          <button
            onClick={handleDownload}
            disabled={isDownloading}
            className={`px-8 py-3 rounded-xl font-bold transition-all shadow-lg flex items-center gap-2 ${
              isDownloading
                ? "bg-gray-700/50 text-gray-400 cursor-not-allowed border border-white/5"
                : "bg-gradient-to-r from-neon-cyan to-blue-600 hover:shadow-neon-cyan/25 hover:scale-105 active:scale-95 text-white"
            }`}
          >
            {isDownloading ? (
              <>
                <span className="animate-spin">⚙️</span> {t("processing")}
              </>
            ) : (
              <>🚀 {t("start")}</>
            )}
          </button>
        </div>
      </div>

      {/* Progress Section */}
      {isDownloading && (
        <div className="bg-graphite-800/40 backdrop-blur-md rounded-2xl p-5 border border-neon-cyan/20 animate-toast-in">
          <div className="flex justify-between items-end mb-3">
            <div>
              <p className="text-neon-cyan text-[10px] font-black uppercase tracking-[0.2em] mb-1">
                Downloader Active
              </p>
              <p className="text-white font-mono text-sm flex items-center gap-2">
                <span className="w-2 h-2 bg-neon-cyan rounded-full animate-pulse"></span>
                Capturing: {new URL(url).hostname}
              </p>
            </div>
            <div className="text-right">
              <span className="text-neon-cyan font-mono text-2xl font-black">
                {downloadPercent}%
              </span>
            </div>
          </div>
          <div className="h-2 w-full bg-black/60 rounded-full overflow-hidden p-[1px] border border-white/5">
            <div
              className="h-full bg-gradient-to-r from-blue-600 to-neon-cyan transition-all duration-500 relative"
              style={{ width: `${downloadPercent}%` }}
            >
              <div className="absolute inset-0 bg-[linear-gradient(90deg,transparent_0%,rgba(255,255,255,0.2)_50%,transparent_100%)] animate-shimmer"></div>
            </div>
          </div>
        </div>
      )}

      {/* Terminal Section */}
      <div className="flex-1 bg-black/90 rounded-2xl border border-white/10 p-4 font-mono text-sm overflow-hidden flex flex-col shadow-2xl relative group">
        <div className="absolute top-0 left-0 right-0 h-10 bg-white/5 flex items-center px-4 gap-2 border-b border-white/5 select-none z-10 transition-colors group-hover:bg-white/10">
          <div className="flex gap-2">
            <div className="w-3 h-3 rounded-full bg-red-500/60"></div>
            <div className="w-3 h-3 rounded-full bg-yellow-500/60"></div>
            <div className="w-3 h-3 rounded-full bg-green-500/60"></div>
          </div>
          <span className="ml-4 text-xs text-gray-500 uppercase tracking-widest">
            {t("terminal")} — WORKER_POOL_ACTIVE
          </span>
        </div>

        <div className="mt-10 flex-1 overflow-y-auto space-y-0.5 p-2 font-mono scrollbar-custom">
          {downloadLogs.length === 0 ? (
            <div className="h-full flex items-center justify-center text-gray-800 italic">
              {t("waiting")}
            </div>
          ) : (
            downloadLogs.map((log, i) => <LogEntry key={i} log={log} />)
          )}
          <div ref={logEndRef} />
        </div>
      </div>
    </div>
  );
};

export default DownloadView;
