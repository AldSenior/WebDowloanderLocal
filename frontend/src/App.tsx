import { useState, useCallback } from 'react';
import Sidebar from "./components/Sidebar";
import DownloadView from "./components/DownloadView";
import LibraryGrid from "./components/LibraryGrid";
import ServerView from "./components/ServerView";
import SettingsView from "./components/SettingsView";
import ToastContainer from "./components/ToastContainer";
import Modal from "./components/Modal";
import { AppProvider, useApp } from "./context/AppContext";

function MainLayout() {
    const [activeTab, setActiveTab] = useState("download");
    const { theme } = useApp();

    const renderContent = useCallback(() => {
        switch (activeTab) {
            case "download":
                return <DownloadView />;
            case "library":
                return <LibraryGrid />;
            case "server":
                return <ServerView />;
            case "settings":
                return <SettingsView />;
            default:
                return <DownloadView />;
        }
    }, [activeTab]);

    return (
        <div id="app" className={`flex h-screen w-screen bg-graphite-900 text-white overflow-hidden font-sans selection:bg-neon-cyan/30 theme-${theme}`}>
            {/* Sidebar */}
            <Sidebar activeTab={activeTab} setActiveTab={setActiveTab} />

            {/* Main Content */}
            <main className="flex-1 p-10 overflow-hidden relative">
                {/* Background Decor */}
                <div className="absolute top-0 right-0 w-[500px] h-[500px] bg-neon-cyan/5 rounded-full blur-[100px] pointer-events-none -translate-y-1/2 translate-x-1/2"></div>

                <div className="h-full w-full relative z-10">
                    {renderContent()}
                </div>
            </main>

            <ToastContainer />
            <Modal />
        </div>
    );
}

function App() {
    return (
        <AppProvider>
            <MainLayout />
        </AppProvider>
    )
}

export default App;
