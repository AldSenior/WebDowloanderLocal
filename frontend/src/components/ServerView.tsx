import React, { useState, useEffect } from 'react';
// @ts-ignore
import { StartServer, StopServer, SelectFolder } from "../../wailsjs/go/main/App";
// @ts-ignore
import { EventsOn } from "../../wailsjs/runtime";
import { useTranslation } from '../i18n';

const ServerView = React.memo(() => {
    const { t } = useTranslation();
    const [status, setStatus] = useState(t('stopped'));
    const [port, setPort] = useState("8080");
    const [directory, setDirectory] = useState("downloads");
    const [isRunning, setIsRunning] = useState(false);
    const [logs, setLogs] = useState<string[]>([]);

    useEffect(() => {
        const cleanupStatus = EventsOn("server:status", (msg: string) => {
            if (msg.includes("Running") || msg.includes("http")) {
                setIsRunning(true);
                setStatus(msg);
                setLogs(prev => [...prev.slice(-100), `[INFO] ${msg}`]);
            } else if (msg === "Stopped") {
                setIsRunning(false);
                setStatus(t('stopped'));
                setLogs(prev => [...prev.slice(-100), `[${t('system')}] ${t('stopped')}`]);
            }
        });
        const cleanupError = EventsOn("server:error", (msg: string) => {
            setIsRunning(false);
            setStatus(t('error'));
            setLogs(prev => [...prev.slice(-100), `[${t('error')}] ${msg}`]);
        });

        return () => {
            cleanupStatus();
            cleanupError();
        };
    }, [t]);

    const toggleServer = React.useCallback(async () => {
        if (isRunning) {
            const res = await StopServer();
            setLogs(prev => [...prev.slice(-100), `[CMD] ${res}`]);
        } else {
            const res = await StartServer(directory, port);
            setLogs(prev => [...prev.slice(-100), `[${t('system')}] ${t('started_at')} ${res}`]);
        }
    }, [isRunning, directory, port, t]);

    const handleSelectFolder = React.useCallback(async () => {
        const folder = await SelectFolder();
        if (folder) setDirectory(folder);
    }, []);

    return (
        <div className="h-full flex flex-col gap-6">
            {/* Control Card */}
            <div className="bg-graphite-800/40 backdrop-blur-md rounded-2xl p-6 border border-white/5 shadow-xl">
                <h2 className="text-2xl font-bold mb-6 bg-clip-text text-transparent bg-gradient-to-r from-neon-green to-blue-400">{t('server')}</h2>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-8">
                    <div>
                        <label className="block text-gray-400 text-sm mb-2 font-mono">{t('port')}</label>
                        <input
                            type="text"
                            value={port}
                            onChange={(e) => setPort(e.target.value)}
                            disabled={isRunning}
                            className="w-full bg-black/40 border border-white/10 rounded-xl px-4 py-3 text-white font-mono focus:border-neon-cyan focus:outline-none disabled:opacity-50"
                        />
                    </div>
                    <div>
                        <label className="block text-gray-400 text-sm mb-2 font-mono">{t('directory')}</label>
                        <div className="flex gap-2">
                            <input
                                type="text"
                                value={directory}
                                onChange={(e) => setDirectory(e.target.value)}
                                disabled={isRunning}
                                className="flex-1 bg-black/40 border border-white/10 rounded-xl px-4 py-3 text-white font-mono focus:border-neon-cyan focus:outline-none disabled:opacity-50"
                            />
                            <button
                                onClick={handleSelectFolder}
                                disabled={isRunning}
                                className="px-4 bg-white/5 hover:bg-white/10 border border-white/10 rounded-xl transition-all disabled:opacity-50"
                            >
                                📂
                            </button>
                        </div>
                    </div>
                </div>

                <div className="flex items-center gap-4">
                    <button
                        onClick={toggleServer}
                        className={`px-8 py-3 rounded-xl font-bold transition-all shadow-lg flex items-center gap-2 ${isRunning
                            ? 'bg-red-500/10 text-red-500 border border-red-500/50 hover:bg-red-500/20'
                            : 'bg-neon-green/10 text-neon-green border border-neon-green/50 hover:bg-neon-green/20'
                            }`}
                    >
                        <div className={`w-3 h-3 rounded-full ${isRunning ? 'bg-red-500 animate-pulse' : 'bg-neon-green'}`}></div>
                        {isRunning ? t('stop_server') : t('start_server')}
                    </button>

                    {isRunning && (
                        <div className="ml-auto text-green-400 font-mono text-sm truncate max-w-[50%]">
                            {status}
                        </div>
                    )}
                </div>
            </div>

            {/* Logs */}
            <div className="flex-1 bg-black/90 rounded-2xl border border-white/10 p-4 font-mono text-sm overflow-hidden flex flex-col">
                <div className="text-gray-500 border-b border-white/5 pb-2 mb-2 text-xs">{t('server_logs')}</div>
                <div className="flex-1 overflow-y-auto space-y-1 scrollbar-custom">
                    {logs.map((log, i) => (
                        <div key={i} className="text-gray-300">{log}</div>
                    ))}
                </div>
            </div>
        </div>
    );
});

export default ServerView;
