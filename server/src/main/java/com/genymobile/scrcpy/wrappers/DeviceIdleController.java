package com.genymobile.scrcpy.wrappers;

import com.genymobile.scrcpy.power.PowerSaveLeaseStore;

import android.annotation.SuppressLint;
import android.os.IBinder;
import android.os.IInterface;

import java.lang.reflect.Method;
import java.util.Arrays;

/** The same deviceidle whitelist used by `dumpsys deviceidle whitelist +/-pkg`. */
@SuppressLint("PrivateApi,DiscouragedPrivateApi")
public final class DeviceIdleController implements PowerSaveLeaseStore.Backend {
    private final IInterface service;
    private final Method read;
    private final Method add;
    private final Method remove;

    public DeviceIdleController() throws ReflectiveOperationException {
        IBinder binder = (IBinder) Class.forName("android.os.ServiceManager").getMethod("getService", String.class).invoke(null, "deviceidle");
        if (binder == null) {
            throw new IllegalStateException("deviceidle service unavailable");
        }
        Class<?> api = Class.forName("android.os.IDeviceIdleController");
        service = (IInterface) Class.forName("android.os.IDeviceIdleController$Stub").getMethod("asInterface", IBinder.class).invoke(null, binder);
        read = api.getMethod("getFullPowerWhitelist");
        add = api.getMethod("addPowerSaveWhitelistApp", String.class);
        remove = api.getMethod("removePowerSaveWhitelistApp", String.class);
    }

    @Override
    public boolean contains(String pkg) throws ReflectiveOperationException {
        String[] packages = (String[]) read.invoke(service);
        if (packages == null) {
            throw new IllegalStateException("deviceidle returned no whitelist");
        }
        return Arrays.asList(packages).contains(pkg);
    }

    @Override
    public void add(String pkg) throws ReflectiveOperationException {
        add.invoke(service, pkg);
    }

    @Override
    public void remove(String pkg) throws ReflectiveOperationException {
        remove.invoke(service, pkg);
    }
}
