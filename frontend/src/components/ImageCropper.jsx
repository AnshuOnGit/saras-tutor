import { useRef, useCallback } from "react";
import Cropper from "react-cropper";
import "cropperjs/dist/cropper.css";

export default function ImageCropper({ imageSrc, onCropped, onCancel }) {
  const cropperRef = useRef(null);

  const handleCrop = useCallback(() => {
    const cropper = cropperRef.current?.cropper;
    if (!cropper) return;

    const canvas = cropper.getCroppedCanvas({
      maxWidth: 1568,
      maxHeight: 1568,
      imageSmoothingQuality: "high",
    });

    canvas.toBlob(
      (blob) => {
        if (blob) {
          const file = new File([blob], "cropped.jpg", { type: "image/jpeg" });
          onCropped(file, URL.createObjectURL(blob));
        }
      },
      "image/jpeg",
      0.92
    );
  }, [onCropped]);

  return (
    <div className="cropper-modal-overlay">
      <div className="cropper-modal">
        <div className="cropper-header">
          <h3>✂️ Crop Image</h3>
          <p>Select the area you want to extract text from</p>
        </div>
        <div className="cropper-container">
          <Cropper
            ref={cropperRef}
            src={imageSrc}
            style={{ height: "100%", width: "100%" }}
            guides={true}
            viewMode={1}
            autoCropArea={0.9}
            responsive={true}
            background={false}
            checkOrientation={true}
          />
        </div>
        <div className="cropper-actions">
          <button className="btn btn-cropper-cancel" onClick={onCancel}>
            ✕ Cancel
          </button>
          <button className="btn btn-cropper-done" onClick={handleCrop}>
            ✓ Crop & Use
          </button>
        </div>
      </div>
    </div>
  );
}
