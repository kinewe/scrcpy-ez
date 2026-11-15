package com.genymobile.scrcpy.video;
import com.genymobile.scrcpy.model.Size;
/** Startup shape follows fresh logical main-display metrics, never a transport cache. */
public final class InitialDisplayLayout {
    private InitialDisplayLayout() {}
    public static Size orientLike(Size requested,Size main) {
        if(requested==null||main==null||requested.getWidth()<=0||requested.getHeight()<=0||main.getWidth()<=0||main.getHeight()<=0)throw new IllegalArgumentException("invalid initial display size");
        if(main.getWidth()==main.getHeight()||requested.getWidth()==requested.getHeight())return requested;
        return (requested.getWidth()>requested.getHeight())==(main.getWidth()>main.getHeight())?requested:requested.rotate();
    }
}
