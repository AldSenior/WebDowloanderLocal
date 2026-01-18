import React, { useMemo } from 'react';
import { useTranslation } from '../i18n';

interface SidebarProps {
    activeTab: string;
    setActiveTab: (tab: string) => void;
}

const Sidebar: React.FC<SidebarProps> = React.memo(({ activeTab, setActiveTab }) => {
    const { t } = useTranslation();
    const menuItems = useMemo(() => [
        { id: 'download', label: t('download'), icon: '⬇️' },
        { id: 'library', label: t('library'), icon: '📚' },
        { id: 'server', label: t('server'), icon: '🌐' },
        { id: 'settings', label: t('settings'), icon: '⚙️' },
    ], [t]);

    return (
        <div className="w-64 h-full bg-graphite-800/50 backdrop-blur-xl border-r border-white/5 flex flex-col p-4 select-none">
            <div className="mb-8 flex items-center gap-3 px-2">
                <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-neon-cyan to-blue-600 shadow-lg shadow-neon-cyan/20"></div>
                <h1 className="text-xl font-bold bg-clip-text text-transparent bg-gradient-to-r from-white to-gray-400">
                    SiteCloner
                </h1>
            </div>

            <nav className="flex-1 space-y-2">
                {menuItems.map((item) => (
                    <button
                        key={item.id}
                        onClick={() => setActiveTab(item.id)}
                        className={`w-full flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-200 group ${activeTab === item.id
                            ? 'bg-neon-cyan/10 text-neon-cyan shadow-inner shadow-neon-cyan/5 border border-neon-cyan/20'
                            : 'text-gray-400 hover:bg-white/5 hover:text-white border border-transparent'
                            }`}
                    >
                        <span className="text-xl group-hover:scale-110 transition-transform duration-200">{item.icon}</span>
                        <span className="font-medium">{item.label}</span>
                        {activeTab === item.id && (
                            <div className="ml-auto w-1.5 h-1.5 rounded-full bg-neon-cyan shadow-[0_0_8px_rgba(14,165,233,0.8)]"></div>
                        )}
                    </button>
                ))}
            </nav>

            <div className="mt-auto pt-4 border-t border-white/5">
                <div className="px-4 py-2 text-xs text-gray-500 font-mono">
                    {t('version')} 2.1.0 Graphite
                </div>
            </div>
        </div>
    );
});

export default Sidebar;
