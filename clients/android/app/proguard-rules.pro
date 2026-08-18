# ONNX Runtime uses reflection and JNI entry points that R8 must preserve.
-keep class ai.onnxruntime.** { *; }
