// Shared notification utility functions
const NotificationUtils = {
    // Get notification icon class with customizable size
    getNotificationIconClass(notification, size = 'w-8 h-8') {
        const baseClass = `${size} rounded-full flex items-center justify-center`;
        
        switch (notification.type) {
            case 'error':
                return `${baseClass} bg-error/20 text-error`;
            case 'warning':
                return `${baseClass} bg-warning/20 text-warning`;
            case 'info':
                return `${baseClass} bg-info/20 text-info`;
            case 'detection':
                return `${baseClass} bg-success/20 text-success`;
            case 'system':
                return `${baseClass} bg-primary/20 text-primary`;
            default:
                return `${baseClass} bg-base-300 text-base-content`;
        }
    },
    
    // Get priority badge class
    getPriorityBadgeClass(priority) {
        switch (priority) {
            case 'critical':
                return 'badge-error';
            case 'high':
                return 'badge-warning';
            case 'medium':
                return 'badge-info';
            case 'low':
                return 'badge-ghost';
            default:
                return 'badge-ghost';
        }
    },
    
    // Format time for display
    formatTimeAgo(timestamp) {
        const date = new Date(timestamp);
        const now = new Date();
        const seconds = Math.floor((now - date) / 1000);
        
        if (seconds < 60) return 'just now';
        if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
        if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
        if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`;
        
        return date.toLocaleDateString();
    },
    
    // Format time for full page (with more detail)
    formatTime(timestamp) {
        const date = new Date(timestamp);
        const now = new Date();
        
        // If today, show time
        if (date.toDateString() === now.toDateString()) {
            return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        }
        
        // If this year, show date without year
        if (date.getFullYear() === now.getFullYear()) {
            return date.toLocaleDateString([], { month: 'short', day: 'numeric' });
        }
        
        // Otherwise show full date
        return date.toLocaleDateString();
    },
    
    // Helper function to safely get CSRF token
    getCSRFToken() {
        const token = document.querySelector('meta[name="csrf-token"]');
        return token ? token.content : '';
    }
};

// Export for use in other files if using modules
if (typeof module !== 'undefined' && module.exports) {
    module.exports = NotificationUtils;
}