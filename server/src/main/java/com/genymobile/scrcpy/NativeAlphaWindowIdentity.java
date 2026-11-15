package com.genymobile.scrcpy;

/** Parse component window names, never labels of attached windows or surfaces. */
final class NativeAlphaWindowIdentity {
    static String activity(String name, String pkg) {
        if (name == null || pkg == null || pkg.isEmpty()) return null;
        int begin = name.indexOf(pkg + "/");
        if (begin < 0) return null;
        // InputWindowHandle prefixes a component with its hex window identity.
        // Panel:/PopupWindow:/SurfaceView labels belong to other IWindow tokens.
        String prefix = name.substring(0, begin);
        if (!prefix.isEmpty() && !prefix.matches("[0-9a-fA-F]+\\s+")) return null;
        String component = name.substring(begin + pkg.length() + 1);
        int end = component.length();
        for (int i = 0; i < component.length(); ++i) {
            char c = component.charAt(i);
            if (c == ' ' || c == '#') { end = i; break; }
        }
        String activity = component.substring(0, end);
        if (!activity.matches("[A-Za-z0-9_.$]+")) return null;
        return activity.startsWith(".") ? pkg + activity : activity;
    }
    private NativeAlphaWindowIdentity() {}
}
