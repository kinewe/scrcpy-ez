package org.scrcpyez.notificationlab;
import android.app.Activity;
import android.os.Bundle;
public class MainActivity extends Activity {
    @Override public void onCreate(Bundle state){super.onCreate(state);Fixture.show(this,"ez synthetic notification fixture");}
}
