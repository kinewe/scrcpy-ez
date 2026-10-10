package org.scrcpyez.notificationlab;
import android.content.*;
public class PostReceiver extends BroadcastReceiver {
    @Override public void onReceive(Context context,Intent command){Fixture.post(context,command);}
}
