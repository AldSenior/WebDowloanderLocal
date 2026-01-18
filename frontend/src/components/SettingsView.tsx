import React from 'react';
import { useTranslation } from '../i18n';
import { useApp } from '../context/AppContext';

const SettingsView = React.memo(() => {
    const { t, lang, setLang } = useTranslation();
    const { theme, setTheme, engineSettings, setEngineSettings, addToast } = useApp();

    const handleThemeChange = React.useCallback((newTheme: 'graphite' | 'ocean' | 'matrix') => {
        setTheme(newTheme);
        addToast(`${t('theme')}: ${newTheme}`, 'success');
    }, [setTheme, addToast, t]);

    const handleLanguageChange = React.useCallback((newLang: 'en' | 'ru') => {
        setLang(newLang);
        addToast(newLang === 'en' ? 'Language changed to English' : 'Язык изменен на Русский', 'info');
    }, [setLang, addToast]);

    return (
        <div className="h-full flex flex-col gap-6 overflow-y-auto pr-4 scrollbar-custom">
            {/* Appearance */}
            <div className="bg-graphite-800/40 backdrop-blur-md rounded-2xl p-6 border border-white/5 shadow-xl">
                <h2 className="text-xl font-bold mb-6 text-white border-b border-white/5 pb-4">{t('appearance')}</h2>

                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                    <button
                        onClick={() => handleThemeChange('graphite')}
                        className={`h-24 rounded-xl bg-graphite-900 border-2 transition-all relative overflow-hidden group ${theme === 'graphite' ? 'border-neon-cyan shadow-[0_0_15px_rgba(14,165,233,0.3)]' : 'border-white/10 opacity-60 hover:opacity-100 hover:border-white/20'}`}
                    >
                        <div className="absolute inset-0 bg-neon-cyan/5 group-hover:bg-neon-cyan/10 transition-colors"></div>
                        <div className="absolute bottom-3 left-3 font-medium text-neon-cyan">Graphite</div>
                        {theme === 'graphite' && <div className="absolute top-2 right-2 w-2 h-2 rounded-full bg-neon-cyan shadow-[0_0_8px_#0EA5E9]"></div>}
                    </button>

                    <button
                        onClick={() => handleThemeChange('ocean')}
                        className={`h-24 rounded-xl bg-[#0f172a] border-2 transition-all relative overflow-hidden group ${theme === 'ocean' ? 'border-blue-400 shadow-[0_0_15px_rgba(56,189,248,0.3)]' : 'border-white/10 opacity-60 hover:opacity-100 hover:border-white/20'}`}
                    >
                        <div className="absolute inset-0 bg-blue-400/5 group-hover:bg-blue-400/10 transition-colors"></div>
                        <div className="absolute bottom-3 left-3 font-medium text-blue-400">Deep Ocean</div>
                        {theme === 'ocean' && <div className="absolute top-2 right-2 w-2 h-2 rounded-full bg-blue-400 shadow-[0_0_8px_#38bdf8]"></div>}
                    </button>

                    <button
                        onClick={() => handleThemeChange('matrix')}
                        className={`h-24 rounded-xl bg-black border-2 transition-all relative overflow-hidden group ${theme === 'matrix' ? 'border-green-500 shadow-[0_0_15px_rgba(16,185,129,0.3)]' : 'border-white/10 opacity-60 hover:opacity-100 hover:border-white/20'}`}
                    >
                        <div className="absolute inset-0 bg-green-500/5 group-hover:bg-green-500/10 transition-colors"></div>
                        <div className="absolute bottom-3 left-3 font-medium text-green-500">Matrix</div>
                        {theme === 'matrix' && <div className="absolute top-2 right-2 w-2 h-2 rounded-full bg-green-500 shadow-[0_0_8px_#10b981]"></div>}
                    </button>
                </div>
            </div>

            {/* Language */}
            <div className="bg-graphite-800/40 backdrop-blur-md rounded-2xl p-6 border border-white/5 shadow-xl">
                <h2 className="text-xl font-bold mb-6 text-white border-b border-white/5 pb-4">{t('language')}</h2>
                <div className="flex gap-4">
                    <button
                        onClick={() => handleLanguageChange('en')}
                        className={`flex-1 py-3 rounded-xl font-bold border transition-all ${lang === 'en' ? 'bg-neon-cyan/10 border-neon-cyan text-neon-cyan shadow-[0_0_10px_rgba(14,165,233,0.2)]' : 'bg-transparent border-white/10 text-gray-400 hover:bg-white/5 hover:border-white/20'}`}
                    >
                        English
                    </button>
                    <button
                        onClick={() => handleLanguageChange('ru')}
                        className={`flex-1 py-3 rounded-xl font-bold border transition-all ${lang === 'ru' ? 'bg-neon-cyan/10 border-neon-cyan text-neon-cyan shadow-[0_0_10px_rgba(14,165,233,0.2)]' : 'bg-transparent border-white/10 text-gray-400 hover:bg-white/5 hover:border-white/20'}`}
                    >
                        Русский
                    </button>
                </div>
            </div>

            {/* Engine Settings */}
            <div className="bg-graphite-800/40 backdrop-blur-md rounded-2xl p-6 border border-white/5 shadow-xl">
                <h2 className="text-xl font-bold mb-6 text-white border-b border-white/5 pb-4">{t('engine_config')}</h2>

                <div className="space-y-6">
                    <div>
                        <div className="flex justify-between mb-2">
                            <label className="text-gray-400 text-sm">{t('workers')}</label>
                            <span className="text-neon-cyan font-mono">{engineSettings.workers}</span>
                        </div>
                        <input
                            type="range" min="1" max="50"
                            value={engineSettings.workers}
                            onChange={(e) => setEngineSettings({ ...engineSettings, workers: parseInt(e.target.value) })}
                            className="w-full h-1.5 bg-gray-700/50 rounded-lg appearance-none cursor-pointer accent-neon-cyan"
                        />
                    </div>

                    <div>
                        <div className="flex justify-between mb-2">
                            <label className="text-gray-400 text-sm">{t('max_depth')}</label>
                            <span className="text-neon-cyan font-mono">{engineSettings.maxDepth}</span>
                        </div>
                        <input
                            type="range" min="1" max="100"
                            value={engineSettings.maxDepth}
                            onChange={(e) => setEngineSettings({ ...engineSettings, maxDepth: parseInt(e.target.value) })}
                            className="w-full h-1.5 bg-gray-700/50 rounded-lg appearance-none cursor-pointer accent-neon-cyan"
                        />
                    </div>

                    <div>
                        <label className="block text-gray-400 text-sm mb-2">{t('user_agent')}</label>
                        <input
                            type="text"
                            value={engineSettings.userAgent}
                            onChange={(e) => setEngineSettings({ ...engineSettings, userAgent: e.target.value })}
                            className="w-full bg-black/40 border border-white/10 rounded-xl px-4 py-3 text-white font-mono text-sm focus:border-neon-cyan/50 focus:outline-none focus:ring-1 focus:ring-neon-cyan/20 transition-all"
                        />
                    </div>
                </div>
            </div>

            <div className="text-center text-gray-600 text-xs mt-4">
                SiteCloner v2.1.0 • Built with Wails & Vite 7
            </div>
        </div>
    );
});

export default SettingsView;
